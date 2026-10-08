package impact

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// declKind is the kind of a top-level declaration.
type declKind int

const (
	kindFunc declKind = iota
	kindMethod
	kindType
	kindConst
	kindVar
	// kindInit is init functions and blank variables, which run at package initialization
	// regardless of references.
	kindInit
)

// decl is a fingerprint of a top-level declaration.
type decl struct {
	kind declKind
	// text is the token sequence of the declaration without comments, including //go: directives.
	text string
	// linkname reports whether the declaration has a //go:linkname directive.
	linkname bool
}

// fileDecls is the declarations of a Go file.
type fileDecls struct {
	pkgName string
	// header is the build constraints, the package name and blank/dot imports, cgo usage.
	// A change of the header may change the package in ways not visible in declarations.
	header string
	// decls maps a local key ("F", "(T).M", "T", "init@file#1") to the declaration.
	decls map[string]decl
}

// parseDecls parses src and returns its top-level declarations.
func parseDecls(filename string, src []byte) (*fileDecls, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	fd := &fileDecls{pkgName: f.Name.Name, decls: make(map[string]decl)}
	fd.header = fileHeader(f)

	tf := fset.File(f.Pos())
	text := func(nodes []ast.Node, docs ...*ast.CommentGroup) (string, bool) {
		var b strings.Builder
		linkname := false
		for _, doc := range docs {
			if doc == nil {
				continue
			}
			for _, c := range doc.List {
				if strings.HasPrefix(c.Text, "//go:") {
					b.WriteString(c.Text)
					b.WriteByte('\n')
					linkname = linkname || strings.HasPrefix(c.Text, "//go:linkname")
				}
			}
		}
		for _, n := range nodes {
			b.WriteString(tokens(src[tf.Offset(n.Pos()):tf.Offset(n.End())]))
			b.WriteByte('\n')
		}
		return b.String(), linkname
	}
	base := filepath.Base(filename)
	inits := 0
	addInit := func(n ast.Node, docs ...*ast.CommentGroup) {
		inits++
		t, ln := text([]ast.Node{n}, docs...)
		fd.decls["init@"+base+"#"+strconv.Itoa(inits)] = decl{kind: kindInit, text: t, linkname: ln}
	}

	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == "init" {
				addInit(d, d.Doc)
				continue
			}
			t, ln := text([]ast.Node{d}, d.Doc)
			if d.Recv == nil {
				fd.decls[d.Name.Name] = decl{kind: kindFunc, text: t, linkname: ln}
			} else if recv := recvTypeName(d.Recv.List[0].Type); recv != "" {
				fd.decls["("+recv+")."+d.Name.Name] = decl{kind: kindMethod, text: t, linkname: ln}
			}
		case *ast.GenDecl:
			fd.addGenDecl(d, text, addInit)
		}
	}
	return fd, nil
}

func (fd *fileDecls) addGenDecl(d *ast.GenDecl, text func([]ast.Node, ...*ast.CommentGroup) (string, bool), addInit func(ast.Node, ...*ast.CommentGroup)) {
	switch d.Tok {
	case token.TYPE:
		for _, s := range d.Specs {
			ts := s.(*ast.TypeSpec)
			t, ln := text([]ast.Node{ts}, d.Doc, ts.Doc)
			fd.decls[ts.Name.Name] = decl{kind: kindType, text: t, linkname: ln}
		}
	case token.CONST, token.VAR:
		kind := kindConst
		if d.Tok == token.VAR {
			kind = kindVar
		}
		// In a const group with iota or implicit repetition, a spec depends on the others.
		whole := d.Tok == token.CONST && len(d.Specs) > 1 && constGroupDependent(d)
		for _, s := range d.Specs {
			vs := s.(*ast.ValueSpec)
			node := ast.Node(vs)
			if whole {
				node = d
			}
			t, ln := text([]ast.Node{node}, d.Doc, vs.Doc)
			for _, name := range vs.Names {
				if name.Name == "_" {
					if d.Tok == token.VAR && len(vs.Values) > 0 {
						addInit(vs, d.Doc, vs.Doc) // initializer runs at package initialization
					}
					continue
				}
				fd.decls[name.Name] = decl{kind: kind, text: t, linkname: ln}
			}
		}
	}
}

func constGroupDependent(d *ast.GenDecl) bool {
	for _, s := range d.Specs {
		vs := s.(*ast.ValueSpec)
		if len(vs.Values) == 0 {
			return true
		}
		usesIota := false
		for _, v := range vs.Values {
			ast.Inspect(v, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == "iota" {
					usesIota = true
				}
				return !usesIota
			})
		}
		if usesIota {
			return true
		}
	}
	return false
}

// recvTypeName returns the receiver base type name, e.g. "T" for *T or T[K].
func recvTypeName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

// fileHeader returns the parts of a file affecting the package beyond its declarations.
func fileHeader(f *ast.File) string {
	var b strings.Builder
	// build constraints: //go:build lines before the package clause
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:build") || strings.HasPrefix(c.Text, "// +build") {
				b.WriteString(c.Text)
				b.WriteByte('\n')
			}
		}
	}
	fmt.Fprintf(&b, "package %s\n", f.Name.Name)
	var imports []string
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		switch {
		case path == "C":
			imports = append(imports, "cgo")
		case imp.Name != nil && (imp.Name.Name == "_" || imp.Name.Name == "."):
			imports = append(imports, imp.Name.Name+" "+path)
		}
	}
	slices.Sort(imports)
	for _, imp := range imports {
		b.WriteString(imp)
		b.WriteByte('\n')
	}
	return b.String()
}

// tokens returns the token sequence of src without comments and formatting.
func tokens(src []byte) string {
	var s scanner.Scanner
	fset := token.NewFileSet()
	s.Init(fset.AddFile("", -1, len(src)), src, nil, 0)
	var b strings.Builder
	semicolon := false
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		// Ignore whether a semicolon is explicit or inserted at a newline, and omit it before ")" and "}"
		// where it is optional, so that "{ return x }" equals "{\n\treturn x\n}".
		if tok == token.SEMICOLON {
			semicolon = true
			continue
		}
		if semicolon && tok != token.RBRACE && tok != token.RPAREN {
			b.WriteString("; ")
		}
		semicolon = false
		b.WriteString(tok.String())
		if lit != "" {
			b.WriteByte(' ')
			b.WriteString(lit)
		}
		b.WriteByte(' ')
	}
	return b.String()
}
