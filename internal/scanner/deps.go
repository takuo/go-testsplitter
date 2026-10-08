package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// DepGraph is the dependency graph of test binaries in the main module(s).
type DepGraph struct {
	// dirs maps a package directory (symlinks resolved) to its import path.
	dirs map[string]string
	// files maps an embedded file (absolute, symlinks resolved) to the import paths embedding it.
	files map[string][]string
	// testDeps maps an import path to the import paths its test binary depends on, including itself.
	testDeps map[string][]string
}

type goListDep struct {
	ImportPath      string
	Dir             string
	ForTest         string
	Deps            []string
	Standard        bool
	Module          *struct{ Main bool }
	EmbedFiles      []string
	TestEmbedFiles  []string
	XTestEmbedFiles []string
}

// LoadDepGraph loads the dependency graph of the packages matched by patterns (see ListPackages)
// with `go list -deps -test`.
func LoadDepGraph(patterns []string) (*DepGraph, error) {
	args := []string{"list", "-deps", "-test", "-json=ImportPath,Dir,ForTest,Deps,Standard,Module,EmbedFiles,TestEmbedFiles,XTestEmbedFiles", "--"}
	for _, p := range patterns {
		args = append(args, normalizePattern(p))
	}
	cmd := exec.Command("go", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run go list: %w: %s", err, stderr.String())
	}

	g := &DepGraph{
		dirs:     make(map[string]string),
		files:    make(map[string][]string),
		testDeps: make(map[string][]string),
	}
	dec := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var p goListDep
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to decode go list output: %w", err)
		}
		if p.Standard || p.Module == nil || !p.Module.Main {
			continue // changes in the repository never affect other modules
		}
		if strings.HasSuffix(p.ImportPath, ".test") && p.ForTest == "" {
			// the test binary: its Deps are all packages linked into it
			imp := strings.TrimSuffix(p.ImportPath, ".test")
			deps := []string{imp}
			for _, d := range p.Deps {
				deps = append(deps, stripTestVariant(d))
			}
			slices.Sort(deps)
			g.testDeps[imp] = slices.Compact(deps)
			continue
		}
		imp := stripTestVariant(p.ImportPath)
		if p.ForTest != "" && strings.TrimSuffix(imp, "_test") == p.ForTest {
			imp = p.ForTest // the package under test or its external test package
		}
		if p.Dir == "" {
			continue
		}
		dir := realPath(p.Dir)
		if p.ForTest == "" {
			g.dirs[dir] = imp
		}
		for _, f := range slices.Concat(p.EmbedFiles, p.TestEmbedFiles, p.XTestEmbedFiles) {
			path := realPath(filepath.Join(p.Dir, f))
			if !slices.Contains(g.files[path], imp) {
				g.files[path] = append(g.files[path], imp)
			}
		}
	}
	return g, nil
}

// stripTestVariant returns the import path without the test variant suffix, e.g. "a [a.test]" → "a".
func stripTestVariant(importPath string) string {
	if i := strings.Index(importPath, " ["); i >= 0 {
		return importPath[:i]
	}
	return importPath
}

func realPath(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	// The file may be deleted: resolve the nearest existing parent.
	dir, base := filepath.Split(filepath.Clean(path))
	if dir == "" || filepath.Clean(dir) == path {
		return path
	}
	return filepath.Join(realPath(filepath.Clean(dir)), base)
}

// ChangedPackages returns import paths of packages affected by changes of files (absolute paths), sorted.
// A file belongs to a package if it is in the package directory, under its testdata directory,
// or embedded by the package. Files belonging to no package are returned as unmatched.
func (g *DepGraph) ChangedPackages(files []string) (packages, unmatched []string) {
	set := make(map[string]bool)
	for _, f := range files {
		path := realPath(f)
		found := false
		for _, imp := range g.files[path] {
			set[imp] = true
			found = true
		}
		if imp, ok := g.dirs[filepath.Dir(path)]; ok {
			set[imp] = true
			found = true
		}
		if imp, ok := g.dirs[testdataOwner(path)]; ok {
			set[imp] = true
			found = true
		}
		if !found {
			unmatched = append(unmatched, f)
		}
	}
	for imp := range set {
		packages = append(packages, imp)
	}
	slices.Sort(packages)
	return packages, unmatched
}

// testdataOwner returns the directory containing the outermost "testdata" directory in path, or "".
func testdataOwner(path string) string {
	elems := strings.Split(filepath.ToSlash(path), "/")
	for i, e := range elems {
		if e == "testdata" && i > 0 {
			return filepath.FromSlash(strings.Join(elems[:i], "/"))
		}
	}
	return ""
}

// AffectedBy returns the changed packages that the test binary of importPath depends on
// (including the package itself), sorted. Empty means the tests are not affected.
func (g *DepGraph) AffectedBy(importPath string, changed []string) []string {
	var via []string
	for _, dep := range g.testDeps[importPath] {
		if _, ok := slices.BinarySearch(changed, dep); ok {
			via = append(via, dep)
		}
	}
	return via
}
