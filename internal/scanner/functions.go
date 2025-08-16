// Package scanner provides scanning functionality for Go packages
package scanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"strings"
)

// ScanTestFunctions scans the specified Go packages for test functions.
func ScanTestFunctions(packages []string) (funcs map[string][]string, err error) {
	funcs = make(map[string][]string)
	fset := token.NewFileSet()

	for _, pkg := range packages {
		log.Printf("Parsing package: %s", pkg)

		// Check if directory exists
		if _, err := os.Stat(pkg); os.IsNotExist(err) {
			log.Printf("Package directory %s does not exist, skipping", pkg)
			continue
		}

		// Parse Go files in the package directory
		pkgs, err := parser.ParseDir(fset, pkg, func(info fs.FileInfo) bool {
			return strings.HasSuffix(info.Name(), "_test.go")
		}, parser.ParseComments)
		if err != nil {
			log.Printf("Failed to parse package %s: %v, skipping", pkg, err)
			continue
		}

		var functions []string
		for _, astPkg := range pkgs {
			for _, file := range astPkg.Files {
				ast.Inspect(file, func(n ast.Node) bool {
					if fn, ok := n.(*ast.FuncDecl); ok {
						if fn.Name.IsExported() && strings.HasPrefix(fn.Name.Name, "Test") && fn.Name.Name != "Test" && fn.Name.Name != "TestMain" {
							functions = append(functions, fn.Name.Name)
						}
					}
					return true
				})
			}
		}

		if len(functions) > 0 {
			funcs[pkg] = functions
			log.Printf("Found %d test functions in package %s", len(functions), pkg)
		} else {
			log.Printf("No test functions found in package %s", pkg)
		}
	}
	log.Printf("Found test functions in %d packages", len(funcs))
	return funcs, nil
}
