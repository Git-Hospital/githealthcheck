package golang

import (
	"go/ast"
	"go/scanner"
	"go/token"
	"hash/fnv"
	"maps"
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
	sc := &scoper{offset: offset, scopes: map[string][][2]int{}, whole: [2]int{offset(fd.Pos()), offset(fd.End())}}
	if fd.Recv != nil {
		sc.receiver(fd.Recv)
	}
	ast.Inspect(fd, sc.visit)
	out := map[int]bool{}
	fixed := fixedIdents(fd)
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == fd.Name {
			return false
		}
		if x, ok := n.(*ast.Ident); ok {
			off := offset(x.Pos())
			if !fixed[x] && slices.ContainsFunc(sc.scopes[x.Name], func(r [2]int) bool { return r[0] <= off && off < r[1] }) {
				out[off] = true
			}
		}
		return true
	})
	return out
}

// scoper records where each name a function declares is local: from its declaration to the end of
// the scope that holds it.
type scoper struct {
	offset func(token.Pos) int
	scopes map[string][][2]int
	whole  [2]int     // the function
	stack  []ast.Node // the scopes the walk is in
}

func (sc *scoper) declare(id *ast.Ident, in [2]int) {
	if id != nil && id.Name != "_" {
		sc.scopes[id.Name] = append(sc.scopes[id.Name], in)
	}
}

// here is from id to the end of the innermost scope.
func (sc *scoper) here(id *ast.Ident) [2]int {
	end := sc.whole[1]
	if len(sc.stack) > 0 {
		end = sc.offset(sc.stack[len(sc.stack)-1].End())
	}
	return [2]int{sc.offset(id.Pos()), end}
}

func (sc *scoper) fields(fl *ast.FieldList, in [2]int) {
	if fl == nil {
		return
	}
	for _, f := range fl.List {
		for _, id := range f.Names {
			sc.declare(id, in)
		}
	}
}

// receiver declares the receiver and the type parameters of a generic receiver, T in (s *Stack[T]).
func (sc *scoper) receiver(recv *ast.FieldList) {
	sc.fields(recv, sc.whole)
	for _, f := range recv.List {
		t := f.Type
		if st, ok := t.(*ast.StarExpr); ok {
			t = st.X
		}
		var params []ast.Expr
		switch x := t.(type) {
		case *ast.IndexExpr:
			params = []ast.Expr{x.Index}
		case *ast.IndexListExpr:
			params = x.Indices
		}
		for _, e := range params {
			if id, ok := e.(*ast.Ident); ok {
				sc.declare(id, sc.whole)
			}
		}
	}
}

func (sc *scoper) visit(n ast.Node) bool {
	if n == nil {
		sc.stack = sc.stack[:len(sc.stack)-1]
		return true
	}
	switch x := n.(type) {
	case *ast.FuncType:
		sc.funcType(x)
	case *ast.AssignStmt:
		if x.Tok == token.DEFINE {
			sc.declareHere(x.Lhs)
		}
	case *ast.ValueSpec:
		for _, id := range x.Names {
			sc.declare(id, sc.here(id))
		}
	case *ast.TypeSpec:
		sc.declare(x.Name, sc.here(x.Name))
	case *ast.RangeStmt:
		if x.Tok == token.DEFINE {
			sc.declareRange(x)
		}
	case *ast.LabeledStmt:
		sc.declare(x.Label, sc.whole)
	}
	sc.stack = append(sc.stack, n)
	return true
}

// funcType declares parameters, which hold for the function or literal the type belongs to.
func (sc *scoper) funcType(x *ast.FuncType) {
	in := sc.whole
	if len(sc.stack) > 0 {
		if fl, ok := sc.stack[len(sc.stack)-1].(*ast.FuncLit); ok {
			in = [2]int{sc.offset(fl.Pos()), sc.offset(fl.End())}
		}
	}
	sc.fields(x.TypeParams, in)
	sc.fields(x.Params, in)
	sc.fields(x.Results, in)
}

// declareHere declares the identifiers among exprs from where each is to the end of the scope.
func (sc *scoper) declareHere(exprs []ast.Expr) {
	for _, l := range exprs {
		if id, ok := l.(*ast.Ident); ok {
			sc.declare(id, sc.here(id))
		}
	}
}

// declareRange declares a range statement's key and value for the statement.
func (sc *scoper) declareRange(x *ast.RangeStmt) {
	for _, e := range []ast.Expr{x.Key, x.Value} {
		if id, ok := e.(*ast.Ident); ok {
			sc.declare(id, [2]int{sc.offset(id.Pos()), sc.offset(x.End())})
		}
	}
}

// fixedIdents returns the identifiers that keep their name in a copy whatever the scopes say: a field
// or method after a dot, and the key of a composite literal other than a map (a struct field).
func fixedIdents(fd *ast.FuncDecl) map[*ast.Ident]bool {
	fixed := map[*ast.Ident]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			fixed[x.Sel] = true
		case *ast.CompositeLit:
			for _, id := range fieldKeys(x) {
				fixed[id] = true
			}
		}
		return true
	})
	return fixed
}

// fieldKeys returns the identifier keys of a composite literal other than a map: struct fields.
func fieldKeys(x *ast.CompositeLit) []*ast.Ident {
	if _, isMap := x.Type.(*ast.MapType); isMap {
		return nil
	}
	var keys []*ast.Ident
	for _, e := range x.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok {
				keys = append(keys, id)
			}
		}
	}
	return keys
}

// tokens returns the token stream of a file after its package clause and imports, without comments or
// semicolons, and sets the span of each function in funcs. Every
// identifier is one kind and every literal another; each keeps a hash of its text, flagged as a
// literal, or as a name local to its function (a copy may rename it) or fixed (a copy must keep it).
func tokens(fset *token.FileSet, src []byte, file *ast.File, funcs []facts.Function) facts.Tokens {
	offset := func(p token.Pos) int { return fset.PositionFor(p, false).Offset }
	start := offset(file.Name.End())
	local := map[int]bool{}
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				start = offset(d.End())
			}
		case *ast.FuncDecl:
			if d.Body != nil {
				maps.Copy(local, locals(d, offset))
			}
		}
	}
	t, offsets := scan(src, start, local)
	setSpans(file, funcs, offsets, offset)
	// Exact sizes: the stream is kept for the whole run.
	t.Kind, t.Name, t.Line = slices.Clone(t.Kind), slices.Clone(t.Name), slices.Clone(t.Line)
	return t
}

// scan tokenizes src from start, and returns the stream and each token's offset.
func scan(src []byte, start int, local map[int]bool) (facts.Tokens, []int32) {
	tf := token.NewFileSet().AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(tf, src, func(token.Position, string) {}, 0)
	var t facts.Tokens
	var offsets []int32
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return t, offsets
		}
		off := tf.Offset(pos)
		if off < start || tok == token.SEMICOLON {
			continue // a semicolon, written or inserted, only separates
		}
		kind, name := uint8(tok), uint32(0)
		switch {
		case tok == token.IDENT:
			kind, name = kindIdent, hash32(lit)&(nameLocal-1)|nameFixed
			if local[off] {
				name = name&^nameFixed | nameLocal
			}
		case tok.IsLiteral():
			kind, name = kindLiteral, hash32(lit)|nameLiteral
		}
		t.Kind = append(t.Kind, kind)
		t.Name = append(t.Name, name)
		t.Line = append(t.Line, int32(tf.PositionFor(pos, false).Line))
		offsets = append(offsets, int32(off))
	}
}

func hash32(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// setSpans sets the token span of each function: funcs are the declarations with a body, in source
// order, as measure lists them. A span runs from the token after the name, so the signature counts,
// to the closing brace.
func setSpans(file *ast.File, funcs []facts.Function, offsets []int32, offset func(token.Pos) int) {
	i := 0
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if i == len(funcs) {
			return
		}
		from, _ := slices.BinarySearch(offsets, int32(offset(fd.Name.End())))
		to, _ := slices.BinarySearch(offsets, int32(offset(fd.Body.Rbrace)))
		if from <= to {
			funcs[i].Span = [2]int32{int32(from), int32(to)}
		}
		i++
	}
}
