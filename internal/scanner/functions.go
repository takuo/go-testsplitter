// Package scanner provides scanning functionality for Go packages
package scanner

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"slices"
	"unicode"
	"unicode/utf8"
)

// ScanTestFunctions parses test files of the packages and returns top-level test functions per package Dir.
// Function names in each package are sorted.
func ScanTestFunctions(packages []Package) (map[string][]string, error) {
	funcs := make(map[string][]string)
	fset := token.NewFileSet()

	for _, pkg := range packages {
		var functions []string
		for _, path := range pkg.TestFiles {
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", path, err)
			}
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && isTestFunc(fn) {
					functions = append(functions, fn.Name.Name)
				}
			}
		}
		if len(functions) == 0 {
			log.Printf("No test functions found in package %s", pkg.Dir)
			continue
		}
		slices.Sort(functions)
		functions = slices.Compact(functions)
		funcs[pkg.Dir] = functions
		log.Printf("Found %d test functions in package %s", len(functions), pkg.Dir)
	}
	log.Printf("Found test functions in %d packages", len(funcs))
	return funcs, nil
}

// isTestFunc reports whether fn is a test function recognized by `go test`:
// func TestXxx(t *testing.T), where Xxx does not start with a lowercase letter.
func isTestFunc(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || fn.Type.TypeParams != nil || fn.Type.Results != nil {
		return false
	}
	if !isTestName(fn.Name.Name) {
		return false
	}
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) > 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

func isTestName(name string) bool {
	const prefix = "Test"
	if len(name) < len(prefix) || name[:len(prefix)] != prefix {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}
