package golang

import (
	"cmp"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// imports lists the packages a file imports.
func imports(fset *token.FileSet, file *ast.File) []facts.Import {
	var out []facts.Import
	for _, is := range file.Imports {
		p, err := strconv.Unquote(is.Path.Value)
		if err != nil {
			continue
		}
		out = append(out, facts.Import{Path: p, Line: lineOf(fset, is.Pos())})
	}
	return out
}

// MaxSmells is the number of smells kept per file. A crafted file can hold one on every other line;
// past this many the rest are counted, not kept.
const MaxSmells = 1000

// smells finds the D32 patterns in a file: error checks that do nothing, placeholders, and comments
// that are code. It returns at most MaxSmells of them, in line order, and the number left out.
func smells(fset *token.FileSet, file *ast.File, code []bool) ([]facts.Smell, int) {
	var out []facts.Smell
	line := func(p token.Pos) int { return lineOf(fset, p) }
	// check finds the error checks in a function body. A function literal is checked under the name
	// of the function that holds it, for empty checks only: a callback such as a filepath.WalkDir
	// function returns nil on an error to skip an entry, by design.
	var check func(body *ast.BlockStmt, results *ast.FieldList, name string)
	check = func(body *ast.BlockStmt, results *ast.FieldList, name string) {
		returnsError := false
		if results != nil && len(results.List) > 0 {
			id, ok := results.List[len(results.List)-1].Type.(*ast.Ident)
			returnsError = ok && id.Name == "error"
		}
		ast.Inspect(body, func(n ast.Node) bool {
			if fl, ok := n.(*ast.FuncLit); ok {
				check(fl.Body, nil, name)
				return false
			}
			is, ok := n.(*ast.IfStmt)
			if !ok || is.Else != nil || !errNotNil(is.Cond) {
				return true
			}
			switch {
			case len(is.Body.List) == 0 && !commented(file, is.Body):
				out = append(out, facts.Smell{Kind: facts.EmptyErrorCheck, Line: line(is.Pos()), EndLine: line(is.End()), Func: name})
			case returnsError && len(is.Body.List) == 1:
				if ret, ok := is.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) > 0 && isNil(ret.Results[len(ret.Results)-1]) {
					out = append(out, facts.Smell{Kind: facts.ErrorDropped, Line: line(is.Pos()), EndLine: line(is.End()), Func: name})
				}
			}
			return true
		})
	}
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			name := funcName(d)
			if placeholder(d.Body) {
				out = append(out, facts.Smell{Kind: facts.Placeholder, Line: line(d.Pos()), EndLine: line(d.End()), Func: name})
			}
			check(d.Body, d.Type.Results, name)
		case *ast.GenDecl:
			// Function literals in package-level variables.
			ast.Inspect(d, func(n ast.Node) bool {
				if fl, ok := n.(*ast.FuncLit); ok {
					check(fl.Body, nil, "a function literal")
					return false
				}
				return true
			})
		}
	}
	for _, cg := range file.Comments {
		for _, sp := range commentedCode(fset, cg, code) {
			out = append(out, facts.Smell{Kind: facts.CommentedCode, Line: sp[0], EndLine: sp[1]})
		}
	}
	slices.SortStableFunc(out, func(a, b facts.Smell) int { return a.Line - b.Line })
	if len(out) > MaxSmells {
		return out[:MaxSmells:MaxSmells], len(out) - MaxSmells
	}
	return out, 0
}

// errNotNil reports `err != nil` and its variants, as ErrCheck does, but only with !=.
func errNotNil(cond ast.Expr) bool {
	b, ok := cond.(*ast.BinaryExpr)
	return ok && b.Op == token.NEQ && ErrCheck(cond)
}

// commented reports whether an empty block holds a comment, which says the emptiness is meant (as
// SonarSource's S108 treats it).
func commented(file *ast.File, b *ast.BlockStmt) bool {
	// The comment groups are in source order: the first after the brace decides.
	i, _ := slices.BinarySearchFunc(file.Comments, b.Lbrace, func(cg *ast.CommentGroup, p token.Pos) int {
		return cmp.Compare(cg.Pos(), p+1)
	})
	return i < len(file.Comments) && file.Comments[i].End() < b.Rbrace
}

// notImplemented matches the messages of a placeholder: one that says so, or one that is a TODO
// ("TODO", "todo: sync"), not one about a to-do item ("todo not found").
var notImplemented = regexp.MustCompile(`(?i)\b(not (yet )?implemented|unimplemented|implement me)\b|^\s*todo\s*($|[:.!-])`)

// placeholder reports a body that only panics or returns an error saying it is not implemented.
func placeholder(body *ast.BlockStmt) bool {
	if len(body.List) != 1 {
		return false
	}
	saysSo := func(args []ast.Expr) bool {
		for _, a := range args {
			if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && notImplemented.MatchString(s) {
					return true
				}
			}
		}
		return false
	}
	call := func(e ast.Expr) *ast.CallExpr { c, _ := e.(*ast.CallExpr); return c }
	switch s := body.List[0].(type) {
	case *ast.ExprStmt:
		if c := call(s.X); c != nil {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "panic" {
				return saysSo(c.Args)
			}
		}
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			if c := call(r); c != nil {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "New" || sel.Sel.Name == "Errorf") && saysSo(c.Args) {
					return true
				}
			}
		}
	}
	return false
}

// directive matches comment lines that are instructions to a tool, not text.
var directive = regexp.MustCompile(`^//(go:|line |export |nolint|lint:|\s*\+build|\s*#)`)

// codeHint is what a line of Go that does something contains: an operator or bracket, or a keyword
// that starts a statement. A line without one is not parsed.
var codeHint = regexp.MustCompile(`[(){}=:<\-+]|^\s*(return|go|defer|for|if|switch|select|var|const|type|func)\b`)

// commentedCode finds runs of // comment lines that, without the slashes, parse as Go statements or
// declarations that do something: a call, an assignment, a declaration, a return or a control
// statement. A line is part of a run when it parses so on its own, or opens or closes a brace, and
// the run counts when it parses as a whole; otherwise its lines count one by one. A comment after
// code on its line is about that code. Lines indented after the slashes, with a tab or two spaces,
// are examples in a doc comment (gofmt indents them with a tab; commented-out Go keeps a single
// space before its own tabs), and directives are instructions to tools: all of these end a run, as
// prose does. A single identifier or a label does not count.
func commentedCode(fset *token.FileSet, cg *ast.CommentGroup, code []bool) [][2]int {
	type line struct {
		n    int
		text string
	}
	var spans [][2]int
	var run []line
	flush := func() {
		defer func() { run = run[:0] }()
		if len(run) == 0 {
			return
		}
		texts := make([]string, len(run))
		for i, l := range run {
			texts[i] = l.text
		}
		if isCode(strings.Join(texts, "\n")) {
			spans = append(spans, [2]int{run[0].n, run[len(run)-1].n})
			return
		}
		for _, l := range run {
			if !strings.HasPrefix(strings.TrimSpace(l.text), "//") && isCode(l.text) {
				spans = append(spans, [2]int{l.n, l.n})
			}
		}
	}
	for _, c := range cg.List {
		t, ok := strings.CutPrefix(c.Text, "//")
		trim := strings.TrimSpace(t)
		if exampleOutput.MatchString(trim) {
			break // the expected output of an example, which go test checks
		}
		n := lineOf(fset, c.Pos())
		switch {
		case !ok || directive.MatchString(c.Text) || strings.HasPrefix(t, "\t") || strings.HasPrefix(t, "  ") || trim == "",
			n < len(code) && code[n]:
			flush()
		case len(run) > 0 && strings.HasPrefix(trim, "//"):
			run = append(run, line{n, t}) // a comment inside commented-out code
		case strings.HasSuffix(trim, "{") || strings.HasPrefix(trim, "}") || codeHint.MatchString(trim) && isCode(trim):
			run = append(run, line{n, t})
		default:
			flush()
		}
	}
	flush()
	return spans
}

// exampleOutput matches the comment that starts the expected output of a testable example.
var exampleOutput = regexp.MustCompile(`^(Unordered output|Output):`)

// isCode reports whether text parses as statements of which one does something, or as declarations
// other than imports.
func isCode(text string) bool {
	if f, err := parser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\n"+text+"\n}\n", parser.SkipObjectResolution); err == nil {
		return slices.ContainsFunc(f.Decls[0].(*ast.FuncDecl).Body.List, doesSomething)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", "package p\n"+text+"\n", parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(f.Decls, func(d ast.Decl) bool {
		g, ok := d.(*ast.GenDecl)
		return !ok || g.Tok != token.IMPORT
	})
}

// doesSomething reports a statement that does something, written as gofmt writes Go: a call has no
// space before its parenthesis (prose such as "Wrap (2)" has), and an assignment assigns to
// something assignable (prose such as "4;2 (semicolon) = dim" does not).
func doesSomething(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ExprStmt:
		switch x := s.X.(type) {
		case *ast.CallExpr:
			return x.Lparen == x.Fun.End()
		case *ast.UnaryExpr:
			return x.Op == token.ARROW
		}
		return false
	case *ast.AssignStmt:
		return !slices.ContainsFunc(s.Lhs, func(e ast.Expr) bool {
			switch e.(type) {
			case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.StarExpr:
				return false
			}
			return true
		})
	case *ast.IncDecStmt, *ast.DeclStmt, *ast.ReturnStmt, *ast.IfStmt, *ast.ForStmt,
		*ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.GoStmt, *ast.DeferStmt,
		*ast.SendStmt:
		return true
	}
	return false
}

// refs lists the names a file uses that could refer to a function of its package: every identifier
// except the names declared by its function declarations, a function's uses of its own name inside
// its body, and the names reachable from outside through //export, //go:linkname and
// //go:wasmexport.
func refs(file *ast.File) []string {
	seen := map[string]bool{}
	for _, d := range file.Decls {
		fd, isFunc := d.(*ast.FuncDecl)
		ast.Inspect(d, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if isFunc && id == fd.Name {
				if fd.Doc != nil && slices.ContainsFunc(fd.Doc.List, func(c *ast.Comment) bool {
					return strings.HasPrefix(c.Text, "//go:wasmexport ")
				}) {
					seen[id.Name] = true // the wasm host calls it
				}
				return true
			}
			if isFunc && fd.Recv == nil && id.Name == fd.Name.Name {
				return true // recursion does not keep a function alive
			}
			seen[id.Name] = true
			return true
		})
	}
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			f := strings.Fields(c.Text)
			switch {
			case len(f) >= 2 && f[0] == "//export":
				seen[f[1]] = true
			case len(f) >= 2 && f[0] == "//go:linkname":
				seen[f[1]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}
