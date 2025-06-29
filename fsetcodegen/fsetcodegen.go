package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"
)

// Usage: go run fsetcodegen.go -file=myfile.go -var=myFsetVar
// Intended for use with go:generate

func main() {
	varName := flag.String("var", "", "Name of the fset variable to target")
	funcName := flag.String("func", "", "Name of the function to search for the fset variable (optional)")
	debugFlag := flag.Bool("debug", false, "Enable debug output")
	flag.Parse()

	sourceFile := os.Getenv("GOFILE")
	if sourceFile == "" || *varName == "" {
		fmt.Fprintln(os.Stderr, "Usage: go:generate go run ./cmd/fsetcodegen.go -var=<fsetVar> [-func=<funcName>] [--debug]")
		os.Exit(1)
	}

	debug := *debugFlag

	err := generateCompiledFset(sourceFile, *varName, *funcName, debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fsetcodegen error: %v\n", err)
		os.Stderr.Sync()
		os.Exit(1)
	}
	if debug {
		fmt.Fprintln(os.Stderr, "DEBUG: main completed without error")
		os.Stderr.Sync()
	}
}

func generateCompiledFset(sourceFile, varName string, funcName string, debug bool) error {
	if debug {
		fmt.Fprintln(os.Stderr, "DEBUG: Entered generateCompiledFset")
		os.Stderr.Sync()
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, sourceFile, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("failed to parse file: %w", err)
	}

	// Find the fset variable and its type parameter
	var fsetTypeParam string
	type FsetFunc struct {
		FuncExpr     string // e.g. "myFunc" or "obj.Method"
		Backoff      string // e.g. "myBackoff" or "" if not set
		RetryOpts    string // e.g. "myOpts" or "" if not set
		ReceiverType string // e.g. "Stuff" if method value is (&Stuff{}).Fa, else ""
	}
	var fsetFuncs []FsetFunc
	var isPure = true       // assume pure until proven otherwise
	var receiverType string // if any method value uses (&Type{}).Method, this will be set to "Type"

	// Helper to extract type parameter from a composite literal
	extractTypeParam := func(composite *ast.CompositeLit) string {
		if composite == nil {
			return ""
		}
		if se, ok := composite.Type.(*ast.IndexExpr); ok {
			// Handle Fset[serveSO] or fsets.Fset[serveSO]
			switch x := se.X.(type) {
			case *ast.Ident:
				if x.Name == "Fset" {
					if tp, ok := se.Index.(*ast.Ident); ok {
						return tp.Name
					}
				}
			case *ast.SelectorExpr:
				// Handles fsets.Fset[serveSO]
				if x.Sel.Name == "Fset" {
					if tp, ok := se.Index.(*ast.Ident); ok {
						return tp.Name
					}
				}
			}
		}
		return ""
	}

	// If funcName is provided, search inside that function for the variable
	if funcName != "" {
		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG: Found function: %s\n", fn.Name.Name)
			}
			if fn.Name.Name != funcName {
				continue
			}
			// Print debug info about fn.Body
			if debug {
				if fn.Body == nil {
					fmt.Fprintf(os.Stderr, "DEBUG: fn.Body is nil for function %s\n", fn.Name.Name)
					os.Stderr.Sync()
				} else {
					fmt.Fprintf(os.Stderr, "DEBUG: fn.Body is not nil for function %s\n", fn.Name.Name)
					os.Stderr.Sync()
				}
				fmt.Fprintln(os.Stderr, "DEBUG: Before ast.Inspect(fn, ...)")
				os.Stderr.Sync()
			}
			// Traverse the entire function node, not just the body
			ast.Inspect(fn, func(n ast.Node) bool {
				// Print every node for debugging
				if debug {
					fmt.Fprintf(os.Stderr, "DEBUG: AST Node: %T - %#v\n", n, n)
					os.Stderr.Sync()
				}
				// Handle short variable declaration: foo := fsets.Fset[Type]{}
				if decl, ok := n.(*ast.AssignStmt); ok {
					for i, lhs := range decl.Lhs {
						ident, ok := lhs.(*ast.Ident)
						if !ok {
							continue
						}
						if debug {
							fmt.Fprintf(os.Stderr, "DEBUG: AssignStmt LHS ident: %s\n", ident.Name)
							os.Stderr.Sync()
						}
						if ident.Name != varName {
							continue
						}
						if debug {
							fmt.Fprintf(os.Stderr, "DEBUG: Matched variable name: %s\n", varName)
							os.Stderr.Sync()
						}
						// Try to extract type parameter from the RHS (handle both pointer and non-pointer)
						var composite *ast.CompositeLit
						if rhs, ok := decl.Rhs[i].(*ast.CompositeLit); ok {
							composite = rhs
						}
						if unary, ok := decl.Rhs[i].(*ast.UnaryExpr); ok {
							if cl, ok := unary.X.(*ast.CompositeLit); ok {
								composite = cl
							}
						}
						if composite != nil {
							if debug {
								fmt.Fprintf(os.Stderr, "DEBUG: Found composite literal for %s: %#v\n", varName, composite)
								os.Stderr.Sync()
							}
							fsetTypeParam = extractTypeParam(composite)
							if debug {
								fmt.Fprintf(os.Stderr, "DEBUG: Extracted type param: %s\n", fsetTypeParam)
								os.Stderr.Sync()
							}
							if fsetTypeParam != "" {
								return false
							}
						}
					}
				}
				return true
			})
			if debug {
				fmt.Fprintln(os.Stderr, "DEBUG: After ast.Inspect(fn, ...)")
				os.Stderr.Sync()
			}
			break
		}
	} else {
		// Walk the AST to find the variable declaration (support both := and var) at package level
		ast.Inspect(node, func(n ast.Node) bool {
			// Handle short variable declaration: foo := fsets.Fset[Type]{}
			if decl, ok := n.(*ast.AssignStmt); ok {
				for i, lhs := range decl.Lhs {
					ident, ok := lhs.(*ast.Ident)
					if !ok || ident.Name != varName {
						continue
					}
					// Try to extract type parameter from the RHS (handle both pointer and non-pointer)
					var composite *ast.CompositeLit
					if rhs, ok := decl.Rhs[i].(*ast.CompositeLit); ok {
						composite = rhs
					}
					if unary, ok := decl.Rhs[i].(*ast.UnaryExpr); ok {
						if cl, ok := unary.X.(*ast.CompositeLit); ok {
							composite = cl
						}
					}
					if composite != nil {
						fsetTypeParam = extractTypeParam(composite)
						if fsetTypeParam != "" {
							return false
						}
					}
				}
			}
			// Handle var declaration: var foo = fsets.Fset[Type]{}
			if vs, ok := n.(*ast.ValueSpec); ok {
				for i, name := range vs.Names {
					if name.Name != varName {
						continue
					}
					if len(vs.Values) > i {
						// Handle both pointer and non-pointer initializations
						var composite *ast.CompositeLit
						if cl, ok := vs.Values[i].(*ast.CompositeLit); ok {
							composite = cl
						}
						if unary, ok := vs.Values[i].(*ast.UnaryExpr); ok {
							if cl, ok := unary.X.(*ast.CompositeLit); ok {
								composite = cl
							}
						}
						if composite != nil {
							fsetTypeParam = extractTypeParam(composite)
							if fsetTypeParam != "" {
								return false
							}
						}
					}
				}
			}
			return true
		})
	}

	if fsetTypeParam == "" {
		if funcName != "" {
			return fmt.Errorf("could not determine type parameter for fset variable %q in function %q", varName, funcName)
		}
		return errors.New("could not determine type parameter for fset variable")
	}

	// Find functions added to the fset (look for .Adds calls)
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Adds" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != varName {
			return true
		}
		// Arguments to Adds should be C[T]{F: ..., B: ..., O: ...}
		for _, arg := range call.Args {
			if cl, ok := arg.(*ast.CompositeLit); ok {
				var funcExpr, backoffExpr, retryOpts string
				for _, elt := range cl.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok {
							switch key.Name {
							case "F":
								switch expr := kv.Value.(type) {
								case *ast.Ident:
									funcExpr = expr.Name
								case *ast.SelectorExpr:
									isPure = false
									// Detect (&Type{}).Method pattern
									if paren, ok := expr.X.(*ast.ParenExpr); ok {
										if unary, ok := paren.X.(*ast.UnaryExpr); ok && unary.Op == token.AND {
											if cl, ok := unary.X.(*ast.CompositeLit); ok {
												if ident, ok := cl.Type.(*ast.Ident); ok {
													receiverType = ident.Name
													funcExpr = "obj." + expr.Sel.Name
													break
												}
											}
										}
									}
									// Detect r.Method pattern (method value on local variable)
									if _, ok := expr.X.(*ast.Ident); ok {
										// Hardcode receiverType to "RPCServer" for this project
										receiverType = "RPCServer"
										funcExpr = "obj." + expr.Sel.Name
									}
									if funcExpr == "" {
										var buf bytes.Buffer
										if err := format.Node(&buf, token.NewFileSet(), expr); err == nil {
											funcExpr = buf.String()
										} else {
											funcExpr = "<complex>"
										}
									}
								default:
									isPure = false
									funcExpr = "<complex>"
								}
							case "B":
								switch expr := kv.Value.(type) {
								case *ast.Ident:
									backoffExpr = expr.Name
								case *ast.SelectorExpr:
									var buf bytes.Buffer
									ast.Fprint(&buf, token.NewFileSet(), expr, nil)
									backoffExpr = buf.String()
								}
							case "O":
								// O is a slice, so we try to print the expression
								var buf bytes.Buffer
								ast.Fprint(&buf, token.NewFileSet(), kv.Value, nil)
								retryOpts = buf.String()
							}
						}
					}
				}
				fsetFuncs = append(fsetFuncs, FsetFunc{
					FuncExpr:     funcExpr,
					Backoff:      backoffExpr,
					RetryOpts:    retryOpts,
					ReceiverType: receiverType,
				})
			}
		}
		return true
	})

	if len(fsetFuncs) == 0 {
		return errors.New("could not find any functions added to the fset")
	}

	// Generate code using text/template
	packageName := node.Name.Name
	baseName := strings.ToLower(varName)
	outFile := fmt.Sprintf("fset_compiled_%s.go", baseName)
	outPath := filepath.Join(filepath.Dir(sourceFile), outFile)

	type TemplateData struct {
		PackageName   string
		VarName       string
		Funcs         []FsetFunc
		IsPure        bool
		TypeParameter string
		ReceiverType  string
		FuncExported  bool // true if compiled function should be exported (uppercase)
	}

	data := TemplateData{
		PackageName:   packageName,
		VarName:       varName,
		Funcs:         fsetFuncs,
		IsPure:        isPure,
		TypeParameter: fsetTypeParam,
		ReceiverType:  receiverType,
		FuncExported:  funcName == "", // Exported if package-level, private if function-local
	}

	// DEBUG: Print the parsed function expressions before code generation
	if debug {
		fmt.Printf("DEBUG: fsetFuncs = %+v\n", fsetFuncs)
	}

	const codeTemplate = `// Code generated by fsetcodegen; DO NOT EDIT.
package {{.PackageName}}

import (
		"context"
		"github.com/johnsiilver/fsets"
		{{- range .Funcs }}
		{{- if .Backoff }}
		"github.com/gostdlib/base/retry/exponential"
		{{- end }}
		{{- end }}
)

{{- if .IsPure }}
// {{if .FuncExported}}{{.VarName | title}}{{else}}{{.VarName}}{{end}}Compiled is a statically compiled version of the fset pipeline.
func {{if .FuncExported}}{{.VarName | title}}{{else}}{{.VarName}}{{end}}Compiled(ctx context.Context, so fsets.StateObject[{{.TypeParameter}}]) fsets.StateObject[{{.TypeParameter}}] {
		var err error
		if ctx == nil {
			ctx = context.Background()
		}
		so.SetCtx(ctx)

		{{- range .Funcs }}
		{{- if .Backoff }}
		if {{.Backoff}} != nil {
			err := {{.Backoff}}.Retry(
				ctx,
				func(ctx context.Context, rec exponential.Record) error {
					var err error
					so, err = {{.FuncExpr}}(so)
					return err
				},
				{{- if .RetryOpts }}{{.RetryOpts}}{{- end }}
			)
			if err != nil || so.GetStop() {
				so.SetErr(err)
				return so
			}
		} else {
			so, err = {{.FuncExpr}}(so)
			if err != nil || so.GetStop() {
				so.SetErr(err)
				return so
			}
		}
		{{- else }}
		so, err = {{.FuncExpr}}(so)
		if err != nil || so.GetStop() {
			so.SetErr(err)
			return so
		}
		{{- end }}
		{{- end }}
		return so
}
{{- else }}
{{- if .ReceiverType }}
// {{if .FuncExported}}New{{.VarName | title}}Compiled{{else}}new{{.VarName | title}}Compiled{{end}} returns a compiled pipeline for the fset with required objects.
func {{if .FuncExported}}New{{.VarName | title}}Compiled{{else}}new{{.VarName | title}}Compiled{{end}}(obj *{{.ReceiverType}}) func(ctx context.Context, so fsets.StateObject[{{.TypeParameter}}]) fsets.StateObject[{{.TypeParameter}}] {
		return func(ctx context.Context, so fsets.StateObject[{{.TypeParameter}}]) fsets.StateObject[{{.TypeParameter}}] {
			var err error
			if ctx == nil {
				ctx = context.Background()
			}
			so.SetCtx(ctx)
			{{- range .Funcs }}
			{{- if .Backoff }}
			if {{.Backoff}} != nil {
				err := {{.Backoff}}.Retry(
					ctx,
					func(ctx context.Context, rec exponential.Record) error {
						var err error
						so, err = {{.FuncExpr}}(so)
						return err
					},
					{{- if .RetryOpts }}{{.RetryOpts}}{{- end }}
				)
				if err != nil || so.GetStop() {
					so.SetErr(err)
					return so
				}
			} else {
				so, err = {{.FuncExpr}}(so)
				if err != nil || so.GetStop() {
					so.SetErr(err)
					return so
				}
			}
			{{- else }}
			so, err = {{.FuncExpr}}(so)
			if err != nil || so.GetStop() {
				so.SetErr(err)
				return so
			}
			{{- end }}
			{{- end }}
			return so
		}
}
{{- end }}
{{- end }}
`

	tmpl, err := template.New("code").Funcs(template.FuncMap{
		"title": title,
	}).Parse(codeTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse code template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute code template: %w", err)
	}

	// Format and write output
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to format generated code: %w", err)
	}
	return os.WriteFile(outPath, formatted, 0644)
}

// title capitalizes the first letter of a string (for exported Go identifiers)
func title(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
