package golang

import (
	"go/ast"
	"go/scanner"
	"go/token"
	"hash/fnv"
	"slices"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// Kinds of the token stream besides go/token's own, which all fit in a byte.
const (
	kindIdent   = uint8(token.IDENT)
	kindLiteral = uint8(token.STRING)
)

// Name flags: a local name, which a copy may rename; a fixed one, which it must keep; a literal.
const (
	nameLocal   = 1 << 30
	nameFixed   = 1 << 31
	nameLiteral = nameLocal | nameFixed
)

// locals returns the offsets of the identifiers of a function that name its own parameters, results,
// receiver, type parameters and locals, or labels. Without types this goes by name and scope: an
// identifier counts when the function declares that name before it in a scope that holds it, and it
// is not a field or method after a dot, nor the key of a composite literal other than a map.
func locals(fd *ast.FuncDecl, offset func(token.Pos) int) map[int]bool {
	scopes := map[string][][2]int{} // where each name is local: from its declaration to its scope's end
	whole := [2]int{offset(fd.Pos()), offset(fd.End())}
	var stack []ast.Node // the scopes the walk is in
	declare := func(id *ast.Ident, in [2]int) {
		if id != nil && id.Name != "_" {
			scopes[id.Name] = append(scopes[id.Name], in)
		}
	}
	here := func(id *ast.Ident) [2]int {
		end := whole[1]
		if len(stack) > 0 {
			end = offset(stack[len(stack)-1].End())
		}
		return [2]int{offset(id.Pos()), end}
	}
	fields := func(fl *ast.FieldList, in [2]int) {
		if fl != nil {
			for _, f := range fl.List {
				for _, id := range f.Names {
					declare(id, in)
				}
			}
		}
	}
	if fd.Recv != nil {
		fields(fd.Recv, whole)
		// The type parameters of a generic receiver, T in (s *Stack[T]).
		for _, f := range fd.Recv.List {
			t := f.Type
			if st, ok := t.(*ast.StarExpr); ok {
				t = st.X
			}
			switch x := t.(type) {
			case *ast.IndexExpr:
				if id, ok := x.Index.(*ast.Ident); ok {
					declare(id, whole)
				}
			case *ast.IndexListExpr:
				for _, e := range x.Indices {
					if id, ok := e.(*ast.Ident); ok {
						declare(id, whole)
					}
				}
			}
		}
	}
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		switch x := n.(type) {
		case *ast.FuncType:
			// Parameters hold for the function or literal the type belongs to.
			in := whole
			if len(stack) > 0 {
				if fl, ok := stack[len(stack)-1].(*ast.FuncLit); ok {
					in = [2]int{offset(fl.Pos()), offset(fl.End())}
				}
			}
			fields(x.TypeParams, in)
			fields(x.Params, in)
			fields(x.Results, in)
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						declare(id, here(id))
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range x.Names {
				declare(id, here(id))
			}
		case *ast.TypeSpec:
			declare(x.Name, here(x.Name))
		case *ast.RangeStmt:
			if x.Tok == token.DEFINE {
				for _, e := range []ast.Expr{x.Key, x.Value} {
					if id, ok := e.(*ast.Ident); ok {
						declare(id, [2]int{offset(id.Pos()), offset(x.End())})
					}
				}
			}
		case *ast.LabeledStmt:
			declare(x.Label, whole)
		}
		stack = append(stack, n)
		return true
	})
	out := map[int]bool{}
	fixed := map[*ast.Ident]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == fd.Name {
			return false
		}
		switch x := n.(type) {
		case *ast.SelectorExpr:
			fixed[x.Sel] = true
		case *ast.CompositeLit:
			if _, isMap := x.Type.(*ast.MapType); !isMap {
				for _, e := range x.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok {
							fixed[id] = true // a struct field
						}
					}
				}
			}
		case *ast.Ident:
			off := offset(x.Pos())
			if !fixed[x] && slices.ContainsFunc(scopes[x.Name], func(r [2]int) bool { return r[0] <= off && off < r[1] }) {
				out[off] = true
			}
		}
		return true
	})
	return out
}

// tokens returns the token stream of a file after its package clause and imports, without comments or
// semicolons, and sets the span of each function in funcs. Every
// identifier is one kind and every literal another; each keeps a hash of its text, flagged as a
// literal, or as a name local to its function (a copy may rename it) or fixed (a copy must keep it).
func tokens(fset *token.FileSet, src []byte, file *ast.File, funcs []facts.Function) facts.Tokens {
	offset := func(p token.Pos) int { return fset.PositionFor(p, false).Offset }
	start := offset(file.Name.End())
	for _, d := range file.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			start = offset(g.End())
		}
	}
	local := map[int]bool{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			for off := range locals(fd, offset) {
				local[off] = true
			}
		}
	}
	tf := token.NewFileSet().AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(tf, src, func(token.Position, string) {}, 0)
	var t facts.Tokens
	var offsets []int32
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		off := tf.Offset(pos)
		if off < start || tok == token.SEMICOLON {
			continue // a semicolon, written or inserted, only separates
		}
		kind, name := uint8(tok), uint32(0)
		switch {
		case tok == token.IDENT:
			h := fnv.New32a()
			h.Write([]byte(lit))
			kind, name = kindIdent, h.Sum32()&(nameLocal-1)|nameFixed
			if local[off] {
				name = name&^nameFixed | nameLocal
			}
		case tok.IsLiteral():
			h := fnv.New32a()
			h.Write([]byte(lit))
			kind, name = kindLiteral, h.Sum32()|nameLiteral
		}
		t.Kind = append(t.Kind, kind)
		t.Name = append(t.Name, name)
		t.Line = append(t.Line, int32(tf.PositionFor(pos, false).Line))
		offsets = append(offsets, int32(off))
	}
	// funcs are the declarations with a body, in source order, as measure lists them.
	i := 0
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if i == len(funcs) {
			break
		}
		// From the token after the name, so the signature counts, to the closing brace.
		from, _ := slices.BinarySearch(offsets, int32(offset(fd.Name.End())))
		to, _ := slices.BinarySearch(offsets, int32(offset(fd.Body.Rbrace)))
		if from <= to {
			funcs[i].Span = [2]int32{int32(from), int32(to)}
		}
		i++
	}
	// Exact sizes: the stream is kept for the whole run.
	t.Kind, t.Name, t.Line = slices.Clone(t.Kind), slices.Clone(t.Name), slices.Clone(t.Line)
	return t
}
