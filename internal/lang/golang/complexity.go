package golang

import (
	"bytes"
	"go/ast"
	"go/scanner"
	"go/token"
	"reflect"
	"slices"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// measure describes every function and method declaration with a body in file.
func measure(fset *token.FileSet, file *ast.File, code []bool) []facts.Function {
	var out []facts.Function
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		f := facts.Function{
			Name:       funcName(fd),
			Line:       lineOf(fset, fd.Pos()),
			EndLine:    lineOf(fset, fd.End()),
			Cognitive:  cognitive(fd, true),
			Cyclomatic: cyclomatic(fd),
			Nesting:    nesting(fd.Body, 0),
			Statements: len(fd.Body.List),
			Method:     fd.Recv != nil,
			Exported:   fd.Name.IsExported(),
		}
		open, end := lineOf(fset, fd.Body.Lbrace), lineOf(fset, fd.Body.Rbrace)
		for l := open + 1; l < end && l < len(code); l++ {
			if code[l] {
				f.Lines++
			}
		}
		out = append(out, f)
	}
	return out
}

// lineOf returns the line of p in the file as it is, not as a //line directive renames it: a scanned
// file can carry such directives to move or hide what is measured.
func lineOf(fset *token.FileSet, p token.Pos) int { return fset.PositionFor(p, false).Line }

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	if t := recvType(fd.Recv.List[0].Type); t != "" {
		return t + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// recvType returns the type name of a receiver: T for T, *T, T[P] and *T[P].
func recvType(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.ParenExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// codeLines reports, by 1-based line, whether the line holds a token other than a comment. The
// scanner inserts no semicolons here, so only the source's own tokens count.
func codeLines(src []byte) []bool {
	fset := token.NewFileSet()
	tf := fset.AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(tf, src, func(token.Position, string) {}, 0)
	lines := make([]bool, bytes.Count(src, []byte{'\n'})+2)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return lines
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue // an automatic semicolon is not in the source
		}
		start := tf.PositionFor(pos, false).Line
		end := start
		if tok == token.STRING && len(lit) > 0 && lit[0] == '`' {
			// A raw string spans every line it covers. The scanner drops carriage returns from the
			// literal, so its end is found in the source.
			off := tf.Offset(pos)
			if i := bytes.IndexByte(src[off+1:], '`'); i >= 0 {
				end = tf.PositionFor(tf.Pos(off+1+i), false).Line
			}
		}
		for l := start; l <= end && l < len(lines); l++ {
			lines[l] = true
		}
	}
}

// cognitive computes the cognitive complexity of a function as G. Ann Campbell defines it
// ("Cognitive Complexity: A new way of measuring understandability", SonarSource, version 1.7,
// 2023): +1 for each break in the linear flow, plus the nesting level for the structures that nest.
// With guards, an error check without an else adds nothing, as in SonarSource's Go analyzer
// (sonar-go CognitiveComplexity.java): `if err != nil { return err }` is how Go handles an error.
// Without types the check is recognised by its shape: an identifier named err, or ending in Err or
// err, compared with nil. Without guards it is the paper's measure, which gocognit computes.
func cognitive(fd *ast.FuncDecl, guards bool) int {
	c := &cog{fd: fd, guards: guards}
	c.block(fd.Body, 0)
	if c.recursive {
		c.total++
	}
	return c.total
}

type cog struct {
	fd        *ast.FuncDecl
	guards    bool
	total     int
	recursive bool
	shadow    *bool
}

func (c *cog) block(b *ast.BlockStmt, nest int) {
	if b != nil {
		for _, s := range b.List {
			c.node(s, nest)
		}
	}
}

// node walks n at nesting level nest, handling the nodes that add complexity or nest, and plain
// children at the same level.
func (c *cog) node(n ast.Node, nest int) {
	if n == nil || c.structure(n, nest) {
		return
	}
	switch n := n.(type) {
	case *ast.BinaryExpr:
		if n.Op == token.LAND || n.Op == token.LOR {
			c.logical(n, nest)
			return
		}
	case *ast.CallExpr:
		if c.callsItself(n) {
			c.recursive = true
		}
	}
	ast.Inspect(n, func(child ast.Node) bool {
		if child == n {
			return true
		}
		c.node(child, nest)
		return false
	})
}

// structure counts and walks a statement that breaks the linear flow, and reports whether n was one.
func (c *cog) structure(n ast.Node, nest int) bool {
	switch n := n.(type) {
	case *ast.IfStmt:
		if !c.guards || n.Else != nil || !ErrCheck(n.Cond) {
			c.total += 1 + nest
		}
		c.ifChain(n, nest)
	case *ast.ForStmt:
		c.total += 1 + nest
		c.node(n.Init, nest)
		c.node(n.Cond, nest)
		c.node(n.Post, nest)
		c.block(n.Body, nest+1)
	case *ast.RangeStmt:
		c.total += 1 + nest
		c.node(n.X, nest)
		c.block(n.Body, nest+1)
	case *ast.SwitchStmt:
		c.total += 1 + nest
		c.node(n.Init, nest)
		c.node(n.Tag, nest)
		c.clauses(n.Body, nest+1)
	case *ast.TypeSwitchStmt:
		c.total += 1 + nest
		c.node(n.Init, nest)
		c.node(n.Assign, nest)
		c.clauses(n.Body, nest+1)
	case *ast.SelectStmt:
		c.total += 1 + nest
		c.clauses(n.Body, nest+1)
	case *ast.BranchStmt:
		if n.Tok == token.GOTO || n.Label != nil && (n.Tok == token.BREAK || n.Tok == token.CONTINUE) {
			c.total++
		}
	case *ast.FuncLit:
		c.block(n.Body, nest+1)
	default:
		return false
	}
	return true
}

// logical counts a chain of && and ||: 1 for each run of the same operator.
func (c *cog) logical(n *ast.BinaryExpr, nest int) {
	var ops []token.Token
	var leaves []ast.Expr
	flatten(n, &ops, &leaves)
	for i, op := range ops {
		if i == 0 || op != ops[i-1] {
			c.total++
		}
	}
	for _, l := range leaves {
		c.node(l, nest)
	}
}

// ifChain walks an if statement whose own increment was counted, and its else branches: each else
// if and else adds 1 and no nesting.
func (c *cog) ifChain(n *ast.IfStmt, nest int) {
	c.node(n.Init, nest)
	c.node(n.Cond, nest)
	c.block(n.Body, nest+1)
	switch e := n.Else.(type) {
	case *ast.IfStmt:
		c.total++
		c.ifChain(e, nest)
	case *ast.BlockStmt:
		c.total++
		c.block(e, nest+1)
	}
}

func (c *cog) clauses(b *ast.BlockStmt, nest int) {
	for _, s := range b.List {
		switch cl := s.(type) {
		case *ast.CaseClause:
			for _, e := range cl.List {
				c.node(e, nest-1)
			}
			for _, st := range cl.Body {
				c.node(st, nest)
			}
		case *ast.CommClause:
			c.node(cl.Comm, nest-1)
			for _, st := range cl.Body {
				c.node(st, nest)
			}
		}
	}
}

// flatten lists the logical operators of a chain of && and || in source order, through parentheses,
// and the operands that are not themselves part of the chain.
func flatten(e ast.Expr, ops *[]token.Token, leaves *[]ast.Expr) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		if b, ok := x.X.(*ast.BinaryExpr); ok && (b.Op == token.LAND || b.Op == token.LOR) {
			flatten(b, ops, leaves)
			return
		}
	case *ast.BinaryExpr:
		if x.Op == token.LAND || x.Op == token.LOR {
			flatten(x.X, ops, leaves)
			*ops = append(*ops, x.Op)
			flatten(x.Y, ops, leaves)
			return
		}
	}
	*leaves = append(*leaves, e)
}

// callsItself reports a direct recursive call: F(...) or F[T](...) in func F, unless F names a local
// of the function, or r.M(...) in a method M with receiver r. Recursion through other functions is
// not seen: that needs the package's call graph.
func (c *cog) callsItself(call *ast.CallExpr) bool {
	name := c.fd.Name.Name
	fun := call.Fun
	for {
		switch f := fun.(type) {
		case *ast.ParenExpr:
			fun = f.X
			continue
		case *ast.IndexExpr:
			fun = f.X
			continue
		case *ast.IndexListExpr:
			fun = f.X
			continue
		}
		break
	}
	switch f := fun.(type) {
	case *ast.Ident:
		return c.fd.Recv == nil && f.Name == name && !c.shadowed()
	case *ast.SelectorExpr:
		if c.fd.Recv == nil || len(c.fd.Recv.List) == 0 || len(c.fd.Recv.List[0].Names) == 0 {
			return false
		}
		x, ok := f.X.(*ast.Ident)
		return ok && f.Sel.Name == name && x.Name == c.fd.Recv.List[0].Names[0].Name && x.Name != "_"
	}
	return false
}

// shadowed reports whether the function declares a parameter, result or local with its own name, so
// that a call by that name is not a call to itself.
func (c *cog) shadowed() bool {
	if c.shadow != nil {
		return *c.shadow
	}
	name := c.fd.Name.Name
	found := declaresIn(c.fd.Type.Params, name) || declaresIn(c.fd.Type.Results, name)
	ast.Inspect(c.fd.Body, func(n ast.Node) bool {
		found = found || declares(n, name)
		return !found
	})
	c.shadow = &found
	return found
}

// declaresIn reports whether a field list names name.
func declaresIn(fl *ast.FieldList, name string) bool {
	return fl != nil && slices.ContainsFunc(fl.List, func(f *ast.Field) bool { return hasName(f.Names, name) })
}

func hasName(ids []*ast.Ident, name string) bool {
	return slices.ContainsFunc(ids, func(id *ast.Ident) bool { return id.Name == name })
}

// declares reports whether n declares name: a := or a range with :=, a var or const, or the
// parameters and results of a function literal.
func declares(n ast.Node, name string) bool {
	isName := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == name
	}
	switch x := n.(type) {
	case *ast.AssignStmt:
		return x.Tok == token.DEFINE && slices.ContainsFunc(x.Lhs, isName)
	case *ast.ValueSpec:
		return hasName(x.Names, name)
	case *ast.FuncType:
		return declaresIn(x.Params, name) || declaresIn(x.Results, name)
	case *ast.RangeStmt:
		return x.Tok == token.DEFINE && (isName(x.Key) || isName(x.Value))
	}
	return false
}

// cyclomatic counts as gocyclo does: 1, plus 1 for each if, for, range, non-default case or select
// clause, and each && and ||, function literals included.
func cyclomatic(fd *ast.FuncDecl) int {
	n := 1
	ast.Inspect(fd.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			n++
		case *ast.CaseClause:
			if x.List != nil {
				n++
			}
		case *ast.CommClause:
			if x.Comm != nil {
				n++
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				n++
			}
		}
		return true
	})
	return n
}

// nesting returns the deepest nesting of if, for, range, switch and select statements under n, from
// depth d. The if of an else if sits at the depth of the if it continues.
func nesting(n ast.Node, d int) int {
	if n == nil || reflect.ValueOf(n).IsNil() {
		return d
	}
	deepest := d
	visit := func(at int, children ...ast.Node) {
		for _, child := range children {
			deepest = max(deepest, nesting(child, at))
		}
	}
	// The headers of a statement (its init, condition and the like) sit at its own depth: a function
	// literal there nests from it.
	switch x := n.(type) {
	case *ast.IfStmt:
		visit(d, x.Init, x.Cond)
		visit(d+1, x.Body)
		switch e := x.Else.(type) {
		case *ast.IfStmt:
			// The else if continues the chain: its depth is this if's.
			visit(d, &ast.IfStmt{Init: e.Init, Cond: e.Cond, Body: e.Body, Else: e.Else})
		case *ast.BlockStmt:
			visit(d+1, e)
		}
		return max(deepest, d+1)
	case *ast.ForStmt:
		visit(d, x.Init, x.Cond, x.Post)
		visit(d+1, x.Body)
		return max(deepest, d+1)
	case *ast.RangeStmt:
		visit(d, x.X)
		visit(d+1, x.Body)
		return max(deepest, d+1)
	case *ast.SwitchStmt:
		visit(d, x.Init, x.Tag)
		visit(d+1, x.Body)
		return max(deepest, d+1)
	case *ast.TypeSwitchStmt:
		visit(d, x.Init, x.Assign)
		visit(d+1, x.Body)
		return max(deepest, d+1)
	case *ast.SelectStmt:
		visit(d+1, x.Body)
		return max(deepest, d+1)
	}
	ast.Inspect(n, func(child ast.Node) bool {
		if child == n || child == nil {
			return true
		}
		visit(d, child)
		return false
	})
	return deepest
}

// ErrCheck reports whether cond has the shape of an error check: an identifier named err, or ending
// in Err or err, compared with nil by != or ==.
func ErrCheck(cond ast.Expr) bool {
	b, ok := cond.(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ && b.Op != token.EQL {
		return false
	}
	x, y := b.X, b.Y
	if isNil(x) {
		x, y = y, x
	}
	id, ok := x.(*ast.Ident)
	return ok && isNil(y) && (id.Name == "err" || strings.HasSuffix(id.Name, "Err") || strings.HasSuffix(id.Name, "err"))
}

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}
