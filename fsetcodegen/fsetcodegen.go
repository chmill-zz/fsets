package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/johnsiilver/fsets/fsetcodegen/call"
	"github.com/johnsiilver/fsets/fsetcodegen/inline"
)

// Usage: go run fsetcodegen.go -file=myfile.go -var=myFsetVar
// Intended for use with go:generate

var (
	varName    = flag.String("var", "", "Name of the fset variable to target")
	funcName   = flag.String("func", "", "Name of the function to search for the fset variable (optional)")
	debug      = flag.Bool("debug", false, "Enable debug output")
	inlineFlag = flag.Bool("inline", false, "Enable inlining of function bodies (default: false)")
)

func main() {
	flag.Parse()

	sourceFile := os.Getenv("GOFILE")
	if sourceFile == "" || *varName == "" {
		fmt.Fprintln(os.Stderr, "Usage: go:generate go run ./cmd/fsetcodegen.go -var=<fsetVar> [-func=<funcName>] [--debug]")
		os.Exit(1)
	}

	if *inlineFlag {
		inline.Gen(sourceFile, *varName, *funcName, *debug)
	} else {
		call.Gen(sourceFile, *varName, *funcName, *debug)
	}
}
