// Package scanner provides scanning functionality for Go packages
package scanner

import (
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"log/slog"
	"slices"
	"unicode"
	"unicode/utf8"
)

// ScanTestFunctions parses test files of the packages and returns the top-level functions run by
// `go test` per package Dir: tests, fuzz targets (in seed corpus mode) and runnable examples.
// Function names in each package are sorted.
func ScanTestFunctions(packages []Package) (map[string][]string, error) {
	funcs := make(map[string][]string)
	fset := token.NewFileSet()

	for _, pkg := range packages {
		var functions []string
		for _, path := range pkg.TestFiles {
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution|parser.ParseComments)
			if err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", path, err)
			}
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && isTestFunc(fn) {
					functions = append(functions, fn.Name.Name)
				}
			}
			functions = append(functions, runnableExamples(file)...)
		}
		if len(functions) == 0 {
			slog.Debug("No test functions found", "package", pkg.Dir)
			continue
		}
		slices.Sort(functions)
		functions = slices.Compact(functions)
		funcs[pkg.Dir] = functions
		slog.Debug("Found test functions", "package", pkg.Dir, "count", len(functions))
	}
	slog.Info("Scanned test functions", "packages", len(funcs))
	return funcs, nil
}

// isTestFunc reports whether fn is a test function or a fuzz target run by `go test`:
// func TestXxx(t *testing.T) or func FuzzXxx(f *testing.F), where Xxx does not start with
// a lowercase letter. Fuzz targets run their seed corpus as a test unless -test.fuzz is given.
// Benchmarks are not run by default and are ignored.
func isTestFunc(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || fn.Type.TypeParams != nil || fn.Type.Results != nil {
		return false
	}
	var param string
	switch {
	case isTestName(fn.Name.Name, "Test"):
		param = "T" // TestMain(m *testing.M) is the entry point, not a test
	case isTestName(fn.Name.Name, "Fuzz"):
		param = "F"
	default:
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
	// The testing package may be imported under another name, or dot-imported,
	// so only the type name is checked, as `go test` does.
	switch t := star.X.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == param
	case *ast.Ident:
		return t.Name == param
	}
	return false
}

// runnableExamples returns the examples of the file which `go test` runs: func ExampleXxx()
// with an output comment (`// Output:`). Examples without one are only compiled.
func runnableExamples(file *ast.File) []string {
	var names []string
	for _, ex := range doc.Examples(file) {
		if ex.Output == "" && !ex.EmptyOutput {
			continue
		}
		names = append(names, "Example"+ex.Name)
	}
	return names
}

// isTestName reports whether name is prefix followed by nothing or a rune which is not
// a lowercase letter, as `go test` recognizes test functions.
func isTestName(name, prefix string) bool {
	if len(name) < len(prefix) || name[:len(prefix)] != prefix {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}
