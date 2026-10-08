package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Package is a Go package which has test files.
type Package struct {
	// Dir is the package directory relative to the current directory, slash-separated (e.g. "api/service/foo").
	// It is also used as the package name of test results.
	Dir string
	// ImportPath is the import path of the package.
	ImportPath string
	// TestFiles is the list of absolute paths of _test.go files (in-package and external) honoring build constraints.
	TestFiles []string
}

type goListPackage struct {
	Dir          string
	ImportPath   string
	TestGoFiles  []string
	XTestGoFiles []string
}

// ListPackages resolves package patterns (import paths, relative directories, or patterns like "./...")
// with `go list` and returns packages which have test files, sorted by Dir.
// Packages whose import path or directory matches exclude are skipped.
//
// A bare relative directory such as "api/foo" is treated as "./api/foo" if it exists,
// because `go list` would interpret it as an import path.
func ListPackages(patterns []string, exclude *regexp.Regexp) ([]Package, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("could not get current directory: %w", err)
	}

	args := []string{"list", "-json=Dir,ImportPath,TestGoFiles,XTestGoFiles", "--"}
	for _, p := range patterns {
		args = append(args, normalizePattern(p))
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = cwd
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run go list: %w: %s", err, stderr.String())
	}

	var packages []Package
	seen := make(map[string]bool)
	dec := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to decode go list output: %w", err)
		}
		if len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			continue
		}
		rel, err := filepath.Rel(cwd, p.Dir)
		if err != nil {
			return nil, fmt.Errorf("failed to get relative path for %s: %w", p.Dir, err)
		}
		rel = filepath.ToSlash(rel)
		if exclude != nil && (exclude.MatchString(p.ImportPath) || exclude.MatchString(rel)) {
			continue
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true

		pkg := Package{Dir: rel, ImportPath: p.ImportPath}
		for _, f := range slices.Concat(p.TestGoFiles, p.XTestGoFiles) {
			pkg.TestFiles = append(pkg.TestFiles, filepath.Join(p.Dir, f))
		}
		packages = append(packages, pkg)
	}
	slices.SortFunc(packages, func(a, b Package) int { return strings.Compare(a.Dir, b.Dir) })
	return packages, nil
}

func normalizePattern(p string) string {
	if filepath.IsAbs(p) || p == "." || p == ".." ||
		strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") {
		return p
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return "./" + p
	}
	return p
}
