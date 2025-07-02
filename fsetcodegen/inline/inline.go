package inline

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

// Usage: go run fsetcodegen.go -file=myfile.go -var=myFsetVar
// Intended for use with go:generate

func Gen(sourceFile, varName, funcName string, debug bool) {
	err := generateCompiledFset(sourceFile, varName, funcName, debug)
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

	// Collect all imports from the original file
	originalImports := map[string]string{}     // import path -> import name ("" if none)
	originalImportNames := map[string]string{} // import name -> import path
	for _, imp := range node.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		originalImports[path] = name
		if name != "" {
			originalImportNames[name] = path
		} else {
			// Default import name is last element of path
			parts := strings.Split(path, "/")
			originalImportNames[parts[len(parts)-1]] = path
		}
	}
	// Always include context and fsets
	originalImportNames["context"] = "context"
	originalImportNames["fsets"] = "github.com/johnsiilver/fsets"

	// Find the fset variable and its type parameter
	var fsetTypeParam string
	type FsetFunc struct {
		FuncExpr     string // e.g. "myFunc" or "obj.Method"
		Backoff      string // e.g. "myBackoff" or "" if not set
		RetryOpts    string // e.g. "myOpts" or "" if not set
		ReceiverType string // e.g. "Stuff" if method value is (&Stuff{}).Fa, else ""
		InlinedBody  string // inlined function body as string
		IsLast       bool   // true if this is the last function in the pipeline
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

	// Helper: recursively transform statements for inlining
	var transformStmt func(stmt ast.Stmt, buf *bytes.Buffer)
	transformStmt = func(stmt ast.Stmt, buf *bytes.Buffer) {
		switch s := stmt.(type) {
		case *ast.BlockStmt:
			buf.WriteString("{\n")
			for _, st := range s.List {
				transformStmt(st, buf)
			}
			buf.WriteString("}\n")
		case *ast.IfStmt:
			buf.WriteString("if ")
			format.Node(buf, token.NewFileSet(), s.Cond)
			buf.WriteString(" ")
			transformStmt(s.Body, buf)
			if s.Else != nil {
				buf.WriteString("else ")
				transformStmt(s.Else, buf)
			}
		case *ast.ForStmt:
			buf.WriteString("for ")
			if s.Init != nil {
				format.Node(buf, token.NewFileSet(), s.Init)
			}
			buf.WriteString("; ")
			if s.Cond != nil {
				format.Node(buf, token.NewFileSet(), s.Cond)
			}
			buf.WriteString("; ")
			if s.Post != nil {
				format.Node(buf, token.NewFileSet(), s.Post)
			}
			buf.WriteString(" ")
			transformStmt(s.Body, buf)
		case *ast.ReturnStmt:
			if len(s.Results) == 2 {
				errExpr := s.Results[1]
				if ident, ok := errExpr.(*ast.Ident); ok && ident.Name == "nil" {
					buf.WriteString("return so\n")
				} else {
					buf.WriteString("err = ")
					format.Node(buf, token.NewFileSet(), errExpr)
					buf.WriteString("\nso.SetErr(err)\nreturn so\n")
				}
			}
			// else: ignore other return forms
		default:
			format.Node(buf, token.NewFileSet(), stmt)
			buf.WriteString("\n")
		}
	}

	// Helper: extract and transform the body of a function for inlining
	extractInlinedBody := func(funcName string, requiredImports map[string]struct{}) string {
		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != funcName {
				continue
			}
			var buf bytes.Buffer
			// Traverse the function body to collect required imports
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				// Look for SelectorExpr (e.g., errors.New)
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if pkgIdent, ok := sel.X.(*ast.Ident); ok {
						// Don't collect "so", "obj", etc.
						if pkgIdent.Name != "so" && pkgIdent.Name != "obj" && pkgIdent.Name != "ctx" {
							requiredImports[pkgIdent.Name] = struct{}{}
						}
					}
				}
				return true
			})
			for i, stmt := range fn.Body.List {
				isLast := i == len(fn.Body.List)-1
				// If last statement is a return so or return so, nil, skip it
				if isLast {
					if ret, ok := stmt.(*ast.ReturnStmt); ok && len(ret.Results) == 2 {
						if ident, ok := ret.Results[0].(*ast.Ident); ok && ident.Name == "so" {
							if ident2, ok2 := ret.Results[1].(*ast.Ident); ok2 && ident2.Name == "nil" {
								continue // skip final return so, nil
							}
						}
					}
				}
				transformStmt(stmt, &buf)
			}
			return buf.String()
		}
		return "// [inlining failed: function not found or unsupported signature]\n"
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
				var funcExpr, backoffExpr, retryOpts, inlinedBody string
				requiredImports := map[string]struct{}{}
				for _, elt := range cl.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok {
							switch key.Name {
							case "F":
								switch expr := kv.Value.(type) {
								case *ast.Ident:
									funcExpr = expr.Name
									inlinedBody = extractInlinedBody(expr.Name, requiredImports)
								case *ast.SelectorExpr:
									isPure = false
									// Detect (&Type{}).Method pattern
									if paren, ok := expr.X.(*ast.ParenExpr); ok {
										if unary, ok := paren.X.(*ast.UnaryExpr); ok && unary.Op == token.AND {
											if cl, ok := unary.X.(*ast.CompositeLit); ok {
												if ident, ok := cl.Type.(*ast.Ident); ok {
													receiverType = ident.Name
													funcExpr = "obj." + expr.Sel.Name
													inlinedBody = extractInlinedBody(expr.Sel.Name, requiredImports)
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
										inlinedBody = extractInlinedBody(expr.Sel.Name, requiredImports)
									}
									if funcExpr == "" {
										var buf bytes.Buffer
										if err := format.Node(&buf, token.NewFileSet(), expr); err == nil {
											funcExpr = buf.String()
										} else {
											funcExpr = "<complex>"
										}
										inlinedBody = "// [inlining failed: complex function value]\n"
									}
								default:
									isPure = false
									funcExpr = "<complex>"
									inlinedBody = "// [inlining failed: unsupported function value]\n"
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
					InlinedBody:  inlinedBody,
					IsLast:       false, // will set true for last after loop
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
		FuncExported  bool     // true if compiled function should be exported (uppercase)
		Imports       []string // sorted list of required imports
		NeedsErrVar   bool     // true if any inlined body uses 'err'
	}

	// Build the final list of imports (import paths), sorted and deduped
	importSet := map[string]struct{}{}
	for name, path := range originalImportNames {
		if path == "" {
			// Try to resolve path from known imports
			if p, ok := originalImports[name]; ok {
				path = name
				if p != "" {
					path = p
				}
			} else {
				// fallback: skip unknown
				continue
			}
		}
		importSet[path] = struct{}{}
	}
	importList := make([]string, 0, len(importSet))
	for path := range importSet {
		importList = append(importList, path)
	}
	// Always include context and fsets
	importList = append(importList, "context", "github.com/johnsiilver/fsets")
	// Deduplicate and sort
	seen := map[string]struct{}{}
	finalImports := []string{}
	for _, imp := range importList {
		if _, ok := seen[imp]; !ok {
			finalImports = append(finalImports, imp)
			seen[imp] = struct{}{}
		}
	}
	// Remove "" if present
	cleanedImports := []string{}
	for _, imp := range finalImports {
		if imp != "" {
			cleanedImports = append(cleanedImports, imp)
		}
	}
	sort.Strings(cleanedImports)

	// Determine if any inlined body uses 'err'
	needsErrVar := false
	for _, fn := range fsetFuncs {
		if strings.Contains(fn.InlinedBody, "err =") || strings.Contains(fn.InlinedBody, "so.SetErr(err)") {
			needsErrVar = true
			break
		}
	}
	// Mark the last FsetFunc as IsLast = true
	if len(fsetFuncs) > 0 {
		fsetFuncs[len(fsetFuncs)-1].IsLast = true
	}

	data := TemplateData{
		PackageName:   packageName,
		VarName:       varName,
		Funcs:         fsetFuncs,
		IsPure:        isPure,
		TypeParameter: fsetTypeParam,
		ReceiverType:  receiverType,
		FuncExported:  funcName == "", // Exported if package-level, private if function-local
		Imports:       cleanedImports,
		NeedsErrVar:   needsErrVar,
	}

	// DEBUG: Print the parsed function expressions before code generation
	if debug {
		fmt.Printf("DEBUG: fsetFuncs = %+v\n", fsetFuncs)
	}

	const codeTemplate = `// Code generated by fsetcodegen; DO NOT EDIT.
package {{.PackageName}}

import (
{{- range .Imports }}
	"{{.}}"
{{- end }}
)

{{- if .IsPure }}
// {{if .FuncExported}}{{.VarName | title}}{{else}}{{.VarName}}{{end}}Compiled is a statically compiled version of the fset pipeline.
func {{if .FuncExported}}{{.VarName | title}}{{else}}{{.VarName}}{{end}}Compiled(ctx context.Context, so fsets.StateObject[{{.TypeParameter}}]) fsets.StateObject[{{.TypeParameter}}] {
		{{- if $.NeedsErrVar }}var err error{{end}}
		if ctx == nil {
			ctx = context.Background()
		}
		so.SetCtx(ctx)

		{{- range .Funcs }}
{{ .InlinedBody -}}
		{{- if not .IsLast }}
		if so.GetStop() {
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
{{.InlinedBody}}
						return err
					},
					{{- if .RetryOpts }}{{.RetryOpts}}{{- end }}
				)
				if err != nil || so.GetStop() {
					so.SetErr(err)
					return so
				}
			} else {
{{.InlinedBody}}
			}
			{{- else }}
{{.InlinedBody}}
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
		"sub":   sub,
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

// sub returns a - b (for template arithmetic)
func sub(a, b int) int { return a - b }
