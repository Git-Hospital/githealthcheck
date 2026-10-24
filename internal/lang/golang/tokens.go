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
// receiver and locals, or labels. Without types this goes by name: an identifier counts when the
// function declares that name, and it is not a field or method after a dot.
func locals(fd *ast.FuncDecl, offset func(token.Pos) int) map[int]bool {
	declared := map[string]bool{}
	fields := func(fl *ast.FieldList) {
		if fl != nil {
			for _, f := range fl.List {
				for _, id := range f.Names {
					declared[id.Name] = true
				}
			}
		}
	}
	fields(fd.Recv)
	ast.Inspect(fd, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncType:
			fields(x.Params)
			fields(x.Results)
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						declared[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range x.Names {
				declared[id.Name] = true
			}
		case *ast.RangeStmt:
			if x.Tok == token.DEFINE {
				for _, e := range []ast.Expr{x.Key, x.Value} {
					if id, ok := e.(*ast.Ident); ok {
						declared[id.Name] = true
					}
				}
			}
		case *ast.LabeledStmt:
			declared[x.Label.Name] = true
		}
		return true
	})
	delete(declared, "_")
	out := map[int]bool{}
	sel := map[*ast.Ident]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == fd.Name {
			return false
		}
		switch x := n.(type) {
		case *ast.SelectorExpr:
			sel[x.Sel] = true
		case *ast.Ident:
			if declared[x.Name] && !sel[x] {
				out[offset(x.Pos())] = true
			}
		}
		return true
	})
	return out
}

// tokens returns the token stream of a file after its package clause and imports, without comments or
// the semicolons the scanner inserts, and sets the body range of each function in funcs. Every
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
		if off < start || tok == token.SEMICOLON && lit == "\n" {
			continue
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
		from, _ := slices.BinarySearch(offsets, int32(offset(fd.Body.Lbrace))+1)
		to, _ := slices.BinarySearch(offsets, int32(offset(fd.Body.Rbrace)))
		if from <= to {
			funcs[i].Body = [2]int32{int32(from), int32(to)}
		}
		i++
	}
	return t
}
