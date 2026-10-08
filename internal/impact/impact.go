// Package impact selects test functions affected by code changes, at the level of top-level declarations.
//
// It builds a reference graph of top-level declarations (functions, methods, types, constants and variables)
// from type-checked sources, finds declarations changed since a base revision, and selects test functions
// which transitively reference them. Changes which may affect a package beyond references fall back to
// selecting all tests of test binaries linking the package.
package impact

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/takuo/go-testsplitter/internal/gitdiff"
	"github.com/takuo/go-testsplitter/internal/scanner"
)

// Options configures Analyze.
type Options struct {
	// Root is the repository root directory.
	Root string
	// Base is the base revision (the merge base).
	Base string
	// Files is the changed files relative to Root, slash-separated.
	Files []string
	// Graph is the package dependency graph of test binaries.
	Graph *scanner.DepGraph
	// Candidates is the import paths of test packages to select tests from.
	Candidates []string
}

// Selection is the tests selected in a test package.
type Selection struct {
	// All reports whether all tests of the package are selected.
	All bool
	// Reason is why all tests are selected.
	Reason string
	// Functions maps a selected test function to a changed symbol it depends on.
	Functions map[string]string
}

// Analysis is the result of Analyze.
type Analysis struct {
	// ChangedSymbols is the changed top-level declarations, e.g. "example.com/m/pkg.F", "example.com/m/pkg.(T).M".
	ChangedSymbols []string
	// Fallbacks maps an import path to why all tests linking the package are selected.
	Fallbacks map[string]string
	// Selections maps a candidate import path to its selected tests. Candidates without selected tests are absent.
	Selections map[string]*Selection
}

// sym is a top-level declaration: "F", "T", "(T).M" in a package.
type sym struct {
	pkg, local string
}

func (s sym) String() string { return s.pkg + "." + s.local }

// Analyze selects test functions affected by the changes.
func Analyze(ctx context.Context, opts Options) (*Analysis, error) {
	g, err := loadRefGraph(ctx, opts.Graph.MainPackages())
	if err != nil {
		return nil, err
	}
	a := &analyzer{opts: opts, refs: g, fallbacks: make(map[string]string), changed: make(map[sym]bool)}
	if err := a.findChanges(ctx); err != nil {
		return nil, err
	}
	affected := a.propagate()
	a.checkInits(affected)
	return a.result(affected), nil
}

type analyzer struct {
	opts      Options
	refs      *refGraph
	fallbacks map[string]string
	changed   map[sym]bool
}

func (a *analyzer) fallback(files []string, reason string) {
	abs := make([]string, 0, len(files))
	for _, f := range files {
		abs = append(abs, filepath.Join(a.opts.Root, filepath.FromSlash(f)))
	}
	pkgs, _ := a.opts.Graph.ChangedPackages(abs)
	for _, p := range pkgs {
		if _, ok := a.fallbacks[p]; !ok {
			a.fallbacks[p] = reason
		}
	}
}

// pkgDecls is the declarations of changed files in a package at the base and the head.
type pkgDecls struct {
	dir, name  string
	base, head map[string]decl
	files      []string
}

func (a *analyzer) findChanges(ctx context.Context) error {
	pkgs := make(map[string]*pkgDecls) // key: dir + "\x00" + package name
	for _, f := range a.opts.Files {
		if !strings.HasSuffix(f, ".go") {
			a.fallback([]string{f}, "non-Go file changed: "+f)
			continue
		}
		abs := filepath.Join(a.opts.Root, filepath.FromSlash(f))
		head, err := os.ReadFile(abs)
		headExists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		base, baseExists, err := gitdiff.Show(ctx, a.opts.Root, a.opts.Base, f)
		if err != nil {
			return err
		}

		var hd, bd *fileDecls
		if headExists {
			if hd, err = parseDecls(abs, head); err != nil {
				a.fallback([]string{f}, "cannot parse "+f)
				continue
			}
		}
		if baseExists {
			if bd, err = parseDecls(abs, base); err != nil {
				a.fallback([]string{f}, "cannot parse "+f+" at the base revision")
				continue
			}
		}
		if reason := headerChange(bd, hd); reason != "" {
			a.fallback([]string{f}, reason+": "+f)
			continue
		}

		dir := scanner.RealPath(filepath.Dir(abs))
		for _, fd := range []*fileDecls{bd, hd} {
			if fd == nil {
				continue
			}
			key := dir + "\x00" + fd.pkgName
			pd := pkgs[key]
			if pd == nil {
				pd = &pkgDecls{dir: dir, name: fd.pkgName, base: map[string]decl{}, head: map[string]decl{}}
				pkgs[key] = pd
			}
			if !slices.Contains(pd.files, f) {
				pd.files = append(pd.files, f)
			}
			target := pd.head
			if fd == bd {
				target = pd.base
			}
			for k, d := range fd.decls {
				target[k] = d
			}
		}
	}

	for _, pd := range pkgs {
		a.diffPackage(pd)
	}
	return nil
}

// headerChange returns why the file changes the package beyond its declarations, or "".
func headerChange(base, head *fileDecls) string {
	switch {
	case base != nil && head != nil:
		if base.header != head.header {
			return "build constraints, package clause, cgo or blank/dot imports changed"
		}
	case base != nil || head != nil:
		fd := base
		if fd == nil {
			fd = head
		}
		if fd.header != "package "+fd.pkgName+"\n" {
			return "file with build constraints, cgo or blank/dot imports added or removed"
		}
	}
	return ""
}

func (a *analyzer) diffPackage(pd *pkgDecls) {
	pkgPath, ok := a.refs.pkgByDir[pd.dir+"\x00"+pd.name]
	if !ok {
		a.fallback(pd.files, "package of changed files is not loaded")
		return
	}
	keys := slices.Collect(maps.Keys(pd.base))
	keys = append(keys, slices.Collect(maps.Keys(pd.head))...)
	slices.Sort(keys)
	for _, k := range slices.Compact(keys) {
		b, inBase := pd.base[k]
		h, inHead := pd.head[k]
		if inBase && inHead && b == h {
			continue
		}
		d := h
		if !inHead {
			d = b
		}
		switch {
		case d.kind == kindInit || b.kind == kindInit:
			a.fallback(pd.files, "init function or blank variable initializer changed")
			return
		case b.linkname || h.linkname:
			a.fallback(pd.files, "declaration with //go:linkname changed")
			return
		}
		a.changed[sym{pkgPath, k}] = true
	}
}

// dynamicCallers returns the declarations through which a concrete method may be called dynamically.
// It returns nil for non-methods and interface methods.
func (g *refGraph) dynamicCallers(s sym) []sym {
	if !strings.HasPrefix(s.local, "(") {
		return nil
	}
	typeName, method, _ := strings.Cut(strings.TrimPrefix(s.local, "("), ").")
	if g.isInterface(s.pkg, typeName) {
		return nil
	}
	if token.IsExported(method) {
		// Exported methods may be called dynamically through interfaces of other packages or reflection
		// (fmt, encoding/json, text/template, ...), which are invisible in references. Treat it as a change
		// of the type, affecting everything referencing the type.
		return []sym{{s.pkg, typeName}}
	}
	// Unexported methods can only be called dynamically through interfaces of the same package.
	var out []sym
	for _, iface := range g.implementedInterfaces(s.pkg, typeName, method) {
		out = append(out, sym{s.pkg, "(" + iface + ")." + method})
	}
	return out
}

func (g *refGraph) isMain(pkg string) bool {
	tp := g.types[pkg]
	return tp != nil && tp.Name() == "main"
}

func (g *refGraph) isInterface(pkg, name string) bool {
	tp := g.types[pkg]
	if tp == nil {
		return false
	}
	tn, ok := tp.Scope().Lookup(name).(*types.TypeName)
	if !ok {
		return false
	}
	_, ok = tn.Type().Underlying().(*types.Interface)
	return ok
}

// propagate returns the declarations transitively referencing changed declarations,
// mapped to a changed declaration they depend on.
func (a *analyzer) propagate() map[sym]sym {
	affected := make(map[sym]sym)
	var queue []sym
	for _, s := range slices.SortedFunc(maps.Keys(a.changed), func(x, y sym) int { return strings.Compare(x.String(), y.String()) }) {
		affected[s] = s
		queue = append(queue, s)
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, r := range slices.Concat(a.refs.referrers[s], a.refs.dynamicCallers(s)) {
			if _, ok := affected[r]; !ok {
				affected[r] = affected[s]
				queue = append(queue, r)
			}
		}
	}
	return affected
}

// checkInits falls back for packages whose initialization references affected declarations,
// since it runs regardless of references (e.g. registering an implementation in init).
func (a *analyzer) checkInits(affected map[sym]sym) {
	for _, pkg := range slices.Sorted(maps.Keys(a.refs.initRefs)) {
		for _, s := range a.refs.initRefs[pkg] {
			if origin, ok := affected[s]; ok {
				if _, exists := a.fallbacks[pkg]; !exists {
					a.fallbacks[pkg] = "package initialization depends on changed " + origin.String()
				}
				break
			}
		}
	}
}

func (a *analyzer) result(affected map[sym]sym) *Analysis {
	res := &Analysis{Fallbacks: a.fallbacks, Selections: make(map[string]*Selection)}
	for s := range a.changed {
		res.ChangedSymbols = append(res.ChangedSymbols, s.String())
	}
	slices.Sort(res.ChangedSymbols)

	fallbackPkgs := slices.Sorted(maps.Keys(a.fallbacks))
	for _, c := range a.opts.Candidates {
		if via := a.opts.Graph.AffectedBy(c, fallbackPkgs); len(via) > 0 {
			res.Selections[c] = &Selection{All: true, Reason: a.fallbacks[via[0]] + " (" + via[0] + ")"}
			continue
		}
		var sel *Selection
		for _, pkg := range []string{c, c + "_test"} {
			if origin, ok := affected[sym{pkg, "TestMain"}]; ok {
				sel = &Selection{All: true, Reason: "TestMain depends on changed " + origin.String()}
				break
			}
		}
		if origin, ok := affected[sym{c, "main"}]; ok && sel == nil && a.refs.isMain(c) {
			// Tests of a command often build and run the command, which is invisible in references.
			sel = &Selection{All: true, Reason: "main function depends on changed " + origin.String()}
		}
		if sel == nil {
			for s, origin := range affected {
				if (s.pkg == c || s.pkg == c+"_test") && strings.HasPrefix(s.local, "Test") && !strings.Contains(s.local, ".") {
					if sel == nil {
						sel = &Selection{Functions: make(map[string]string)}
					}
					sel.Functions[s.local] = origin.String()
				}
			}
		}
		if sel != nil {
			res.Selections[c] = sel
		}
	}
	return res
}

// refGraph is the reference graph of top-level declarations in the main module.
type refGraph struct {
	// referrers maps a declaration to the declarations referencing it.
	referrers map[sym][]sym
	// initRefs maps a package to the declarations its init functions and blank variables reference.
	initRefs map[string][]sym
	// pkgByDir maps a directory and package name to the package path.
	pkgByDir map[string]string
	types    map[string]*types.Package
}

func loadRefGraph(ctx context.Context, patterns []string) (*refGraph, error) {
	cfg := &packages.Config{
		Context: ctx,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}
	var errs []error
	for _, p := range pkgs {
		for _, e := range p.Errors {
			errs = append(errs, fmt.Errorf("%s: %s", p.ID, e.Msg))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to type-check packages: %w", errors.Join(errs...))
	}

	g := &refGraph{
		referrers: make(map[sym][]sym),
		initRefs:  make(map[string][]sym),
		pkgByDir:  make(map[string]string),
		types:     make(map[string]*types.Package),
	}
	edges := make(map[[2]sym]bool)
	initSeen := make(map[string]map[sym]bool)
	seenFiles := make(map[string]bool)
	for _, p := range pkgs {
		if strings.HasSuffix(p.PkgPath, ".test") || len(p.GoFiles) == 0 {
			continue // generated test main package
		}
		g.pkgByDir[scanner.RealPath(filepath.Dir(p.GoFiles[0]))+"\x00"+p.Name] = p.PkgPath
		if prev, ok := g.types[p.PkgPath]; !ok || len(p.Types.Scope().Names()) > len(prev.Scope().Names()) {
			g.types[p.PkgPath] = p.Types // prefer the test variant, which includes test files
		}
		for _, f := range p.Syntax {
			fileKey := p.PkgPath + "\x00" + p.Fset.File(f.Pos()).Name()
			if seenFiles[fileKey] {
				continue // the same file in another variant
			}
			seenFiles[fileKey] = true
			g.addFile(p, f, edges, initSeen)
		}
	}
	return g, nil
}

func (g *refGraph) addFile(p *packages.Package, f *ast.File, edges map[[2]sym]bool, initSeen map[string]map[sym]bool) {
	info := p.TypesInfo
	uses := func(n ast.Node) []sym {
		var out []sym
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if obj := info.Uses[id]; obj != nil {
					if s, ok := objSym(obj); ok {
						out = append(out, s)
					}
				}
			}
			return true
		})
		return out
	}
	addEdges := func(from sym, n ast.Node) {
		for _, to := range uses(n) {
			if to != from && !edges[[2]sym{from, to}] {
				edges[[2]sym{from, to}] = true
				g.referrers[to] = append(g.referrers[to], from)
			}
		}
	}
	addInit := func(n ast.Node) {
		if initSeen[p.PkgPath] == nil {
			initSeen[p.PkgPath] = make(map[sym]bool)
		}
		for _, s := range uses(n) {
			if !initSeen[p.PkgPath][s] {
				initSeen[p.PkgPath][s] = true
				g.initRefs[p.PkgPath] = append(g.initRefs[p.PkgPath], s)
			}
		}
	}

	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == "init" {
				addInit(d)
				continue
			}
			if s, ok := objSym(info.Defs[d.Name]); ok {
				addEdges(s, d)
			}
		case *ast.GenDecl:
			whole := d.Tok == token.CONST && len(d.Specs) > 1 && constGroupDependent(d)
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if s, ok := objSym(info.Defs[spec.Name]); ok {
						addEdges(s, spec)
					}
				case *ast.ValueSpec:
					node := ast.Node(spec)
					if whole {
						node = d
					}
					for _, name := range spec.Names {
						if name.Name == "_" {
							addInit(spec)
							continue
						}
						if s, ok := objSym(info.Defs[name]); ok {
							addEdges(s, node)
						}
					}
				}
			}
		}
	}
}

// objSym returns the top-level declaration of obj. Methods are "(T).M" of their receiver base type.
func objSym(obj types.Object) (sym, bool) {
	if obj == nil || obj.Pkg() == nil {
		return sym{}, false
	}
	pkg := obj.Pkg().Path()
	if fn, ok := obj.(*types.Func); ok {
		fn = fn.Origin()
		if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
			t := recv.Type()
			if ptr, ok := t.(*types.Pointer); ok {
				t = ptr.Elem()
			}
			named, ok := types.Unalias(t).(*types.Named)
			if !ok {
				return sym{}, false
			}
			return sym{pkg, "(" + named.Origin().Obj().Name() + ")." + fn.Name()}, true
		}
	}
	if obj.Parent() != obj.Pkg().Scope() {
		return sym{}, false // local objects, fields
	}
	return sym{pkg, obj.Name()}, true
}

// implementedInterfaces returns interfaces in pkg having the method which typeName (or its pointer) implements.
func (g *refGraph) implementedInterfaces(pkg, typeName, method string) []string {
	tp := g.types[pkg]
	if tp == nil {
		return nil
	}
	tn, ok := tp.Scope().Lookup(typeName).(*types.TypeName)
	if !ok {
		return nil
	}
	var out []string
	for _, name := range tp.Scope().Names() {
		itn, ok := tp.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		iface, ok := itn.Type().Underlying().(*types.Interface)
		if !ok {
			continue
		}
		if obj, _, _ := types.LookupFieldOrMethod(iface, false, tp, method); obj == nil {
			continue
		}
		if types.Implements(tn.Type(), iface) || types.Implements(types.NewPointer(tn.Type()), iface) {
			out = append(out, name)
		}
	}
	return out
}
