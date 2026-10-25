# The githealthcheck Python helper. githealthcheck runs it as `python3 -I -S -B -X utf8 -c <this>`
# and sends it source files on stdin, each as a header line "<length> <flags>\n" followed by that
# many bytes; flags hold "t" when the file's tokens are wanted. For each file it writes one line of
# JSON with the file's facts. It only parses: nothing it is sent is run, imported or compiled to
# bytecode. It uses the standard library only.

import ast
import collections
import io
import json
import keyword
import re
import sys
import token
import tokenize

sys.setrecursionlimit(4000)

# The address space a helper may use. A crafted file of 1 MiB can make ast take about 1 GB; past the
# bound the file gets a MemoryError and the helper is replaced. Only Linux enforces it.
MAX_MEMORY = 1 << 30

LOCAL = 1 << 30
FIXED = 1 << 31
LITERAL = LOCAL | FIXED
MASK = LOCAL - 1

KIND_IDENT = 4
KIND_LITERAL = 9
KIND_KEYWORD = 100  # + the keyword's index in keyword.kwlist
KIND_OP = 140  # + the operator's exact token type
KIND_INDENT = 250
KIND_DEDENT = 251
KIND_OTHER = 252

SKIPPED = {token.COMMENT, token.NL, token.NEWLINE, token.ENCODING, token.ENDMARKER}
STRING_STARTS = {getattr(token, n) for n in ("FSTRING_START", "TSTRING_START") if hasattr(token, n)}
STRING_ENDS = {getattr(token, n) for n in ("FSTRING_END", "TSTRING_END") if hasattr(token, n)}
KEYWORDS = {k: i for i, k in enumerate(keyword.kwlist)}

# A token as kept: tokenize's own carry their line, which for a long line is most of the memory.
Tok = collections.namedtuple("Tok", "type string start end exact_type")

# The length from which a line is too long for tokenize before Python 3.13, in bytes.
LONG_LINE = 16384

FUNCS = (ast.FunctionDef, ast.AsyncFunctionDef)
SCOPES = (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)


def utf8_lines(src):
    """The source's lines as ast counts them: lines end at \n, \r or \r\n, and a column is a byte
    offset in the line encoded as UTF-8, whatever the file's own encoding."""
    try:
        enc, _ = tokenize.detect_encoding(io.BytesIO(src).readline)
        text = src.decode(enc, "replace")
    except (SyntaxError, ValueError, LookupError):
        text = src.decode("utf-8", "replace")
    return text.encode("utf-8", "replace").splitlines()


def fnv32(data):
    h = 0x811C9DC5
    for b in data:
        h = ((h ^ b) * 0x01000193) & 0xFFFFFFFF
    return h


def limit_memory():
    try:
        import resource

        _, hard = resource.getrlimit(resource.RLIMIT_AS)
        soft = MAX_MEMORY if hard == resource.RLIM_INFINITY else min(MAX_MEMORY, hard)
        resource.setrlimit(resource.RLIMIT_AS, (soft, hard))
    except (ImportError, ValueError, OSError):
        pass


def main():
    limit_memory()
    out = sys.stdout
    out.write("githealthcheck-python %d.%d.%d\n" % sys.version_info[:3])
    out.flush()
    inp = sys.stdin.buffer
    while True:
        header = inp.readline()
        if not header:
            return
        length, flags = header.split()
        n = int(length)
        src = b""
        while len(src) < n:
            chunk = inp.read(n - len(src))
            if not chunk:
                return
            src += chunk
        try:
            resp = File(src, b"t" in flags).facts()
        except (RecursionError, MemoryError) as e:
            resp = {"error": type(e).__name__}
        except Exception as e:  # a helper bug must not stop the scan; the file is partial
            resp = {"error": type(e).__name__}
        out.write(json.dumps(resp, separators=(",", ":")))
        out.write("\n")
        out.flush()


class File:
    def __init__(self, src, want_tokens):
        self.src = src
        self.want_tokens = want_tokens
        self.partial = False
        self.blines = utf8_lines(src)
        # Every token tokenize gives, until it stops, without the line it is on.
        self.toks = []
        # Lines that hold code, and lines that hold a comment.
        self.code, self.comments = set(), {}
        if sys.version_info < (3, 13) and any(len(line) > LONG_LINE for line in self.blines):
            # Before 3.13 tokenize builds each token's line anew, so a long line costs its length for
            # every token on it: the lines are classified by their text, and there are no tokens.
            self.partial = True
            for n, line in enumerate(self.blines, 1):
                text = line.strip()
                if text.startswith(b"#"):
                    self.comments[n] = text.decode("utf-8", "replace")
                elif text:
                    self.code.add(n)
        else:
            try:
                for t in tokenize.tokenize(io.BytesIO(src).readline):
                    self.toks.append(Tok(t.type, t.string, t.start, t.end, t.exact_type))
            except (tokenize.TokenError, SyntaxError, ValueError):
                self.partial = True
            for t in self.toks:
                if t.type == token.COMMENT:
                    self.comments[t.start[0]] = t.string
                elif t.type not in SKIPPED and t.type not in (token.INDENT, token.DEDENT):
                    self.code.update(range(t.start[0], t.end[0] + 1))
        try:
            self.tree = ast.parse(src)
        except (SyntaxError, ValueError):
            self.tree = None
            self.partial = True

    def facts(self):
        resp = {"partial": self.partial, "parsed": self.tree is not None}
        if self.tree is not None:
            parents = {}
            for node in ast.walk(self.tree):
                for child in ast.iter_child_nodes(node):
                    parents[child] = node
            self.parents = parents
            self.scan_scopes()
            resp["i"] = self.imports()
            resp["r"] = self.refs()
            funcs = self.functions()
            resp["f"] = [self.measure(fn, name, method) for fn, name, method in funcs]
            resp["sm"] = self.smells(funcs)
        if self.want_tokens:
            k, n, l, pos = self.tokens()
            resp["tk"], resp["tn"], resp["tl"] = k, n, l
            if self.tree is not None and "f" in resp:
                for f, (fn, _, _) in zip(resp["f"], funcs):
                    f["sp"] = span(fn, pos)
        return resp

    # Functions

    def functions(self):
        """Every function or method that is not inside another function, in source order, with its
        name (Class.method for a method) and whether it is a method."""
        out = []

        def visit(node, prefix, in_class):
            for child in ast.iter_child_nodes(node):
                if isinstance(child, FUNCS):
                    out.append((child, prefix + child.name, in_class))
                elif isinstance(child, ast.ClassDef):
                    visit(child, prefix + child.name + ".", True)
                elif isinstance(child, ast.stmt):
                    visit(child, prefix, False if not in_class else in_class)

        visit(self.tree, "", False)
        out.sort(key=lambda f: (f[0].lineno, f[0].col_offset))
        return out

    def measure(self, fn, name, method):
        body = without_docstring(fn.body)
        lines = 0
        if body:
            doc = docstring_lines(fn)
            for n in range(body[0].lineno, fn.end_lineno + 1):
                if n in self.code and n not in doc:
                    lines += 1
        short = fn.name
        return {
            "n": name,
            "l": fn.lineno,
            "e": fn.end_lineno,
            "c": lines,
            "s": len(body),
            "cog": cognitive(fn, self.blines),
            "cyc": cyclomatic(fn),
            "nest": nesting(fn.body, 0, self.blines),
            "m": method,
            "x": not short.startswith("_") or (short.startswith("__") and short.endswith("__")),
        }

    # Imports and references

    def imports(self):
        out = []
        for node in ast.walk(self.tree):
            if isinstance(node, ast.Import):
                for a in node.names:
                    out.append({"p": a.name, "l": node.lineno, "o": self.optional(node)})
            elif isinstance(node, ast.ImportFrom):
                path = "." * (node.level or 0) + (node.module or "")
                out.append({"p": path, "l": node.lineno, "o": self.optional(node)})
        out.sort(key=lambda i: i["l"])
        return out

    def optional(self, node):
        """Whether an import sits where its failure is expected: in the body of a try that catches
        ImportError or broader, or in a with contextlib.suppress(ImportError)."""
        child, parent = node, self.parents.get(node)
        while parent is not None:
            if isinstance(parent, ast.Try) and self.in_body(child, parent):
                if any(catches_import_error(h.type) for h in parent.handlers):
                    return True
            if isinstance(parent, (ast.With, ast.AsyncWith)) and self.in_body(child, parent):
                for item in parent.items:
                    c = item.context_expr
                    if isinstance(c, ast.Call) and dotted(c.func).endswith("suppress") and any(
                        catches_import_error(a) for a in c.args
                    ):
                        return True
            if isinstance(parent, SCOPES + (ast.ClassDef,)):
                return False
            child, parent = parent, self.parents.get(parent)
        return False

    def in_body(self, child, parent):
        """Whether child is a statement of parent's body, in constant time: a try of 30,000 imports
        would otherwise cost 30,000 squared comparisons."""
        ids = self.bodies.get(id(parent))
        if ids is None:
            ids = self.bodies[id(parent)] = {id(c) for c in parent.body}
        return id(child) in ids

    def refs(self):
        """Every name the file uses that could name a function: identifiers, attribute names, names
        imported from a module, and strings that are identifiers (__all__, getattr). A function's
        calls of itself do not count."""
        seen = set(self.used_names)
        for node in ast.walk(self.tree):
            if isinstance(node, ast.Attribute):
                seen.add(node.attr)
            elif isinstance(node, ast.alias):
                seen.add(node.name.split(".")[0])
                if node.asname:
                    seen.add(node.asname)
                parent = self.parents.get(node)
                if isinstance(parent, ast.ImportFrom):
                    seen.add(node.name)
            elif isinstance(node, ast.Constant) and isinstance(node.value, str) and node.value.isidentifier():
                seen.add(node.value)
        return sorted(seen)

    def scan_scopes(self):
        """One pass over the tree, top down, for what needs the functions around each name: the
        positions of the names that are a parameter or local of a function (or lambda) around them,
        and the names used other than by a function calling itself. A pass from each name up its
        parents would cost the depth of the tree for every name, which a long expression makes
        quadratic."""
        local, used = set(), set()
        self.bodies = {}
        stack = [(self.tree, ())]
        while stack:
            node, scopes = stack.pop()
            if isinstance(node, ast.Name):
                for sc in reversed(scopes):
                    if node.id in scope_locals(sc):
                        local.add((node.lineno, node.col_offset))
                        break
                fn = next((sc for sc in reversed(scopes) if isinstance(sc, FUNCS)), None)
                if fn is None or fn.name != node.id:
                    used.add(node.id)
            elif isinstance(node, ast.arg):
                local.add((node.lineno, node.col_offset))
            inner = scopes + (node,) if isinstance(node, SCOPES) else scopes
            for c in ast.iter_child_nodes(node):
                stack.append((c, inner))
        LOCALS_CACHE.clear()  # it holds the file's tree
        self.local_names, self.used_names = local, used

    # Smells (D32)

    def smells(self, funcs):
        out = []
        names = {fn: name for fn, name, _ in funcs}
        for node in ast.walk(self.tree):
            if isinstance(node, ast.ExceptHandler) and broad(node.type) and ignores(node.body):
                if not any(n in self.comments for n in range(node.lineno, node.end_lineno + 1)):
                    out.append({"k": "except_ignored", "l": node.lineno, "e": node.end_lineno,
                                "f": names.get(self.outer_function(node), "")})
        for fn, name, method in funcs:
            if self.placeholder(fn, method):
                out.append({"k": "placeholder", "l": fn.lineno, "e": fn.end_lineno, "f": name})
        for start, end in self.commented_code():
            out.append({"k": "commented_code", "l": start, "e": end, "f": ""})
        out.sort(key=lambda s: s["l"])
        return out

    def outer_function(self, node):
        found, p = None, self.parents.get(node)
        while p is not None:
            if isinstance(p, FUNCS):
                found = p
            p = self.parents.get(p)
        return found

    def placeholder(self, fn, method):
        """A body that only says it is not implemented: raise NotImplementedError in a function that
        is not a method (a method that does is how a base class asks for an override), a raise whose
        message says so or is a TODO, or pass or ... under a TODO comment. Decorated functions
        (abstract methods, overloads, registered handlers) are left out."""
        if fn.decorator_list:
            return False
        body = without_docstring(fn.body)
        if len(body) != 1:
            return False
        s = body[0]
        if isinstance(s, ast.Raise) and s.exc is not None:
            exc = s.exc.func if isinstance(s.exc, ast.Call) else s.exc
            messages = [a.value for a in getattr(s.exc, "args", []) if isinstance(a, ast.Constant) and isinstance(a.value, str)]
            if isinstance(exc, ast.Name) and exc.id == "NotImplementedError":
                # In a method it is how a base class asks for an override, whatever it says, unless
                # it says TODO.
                if method:
                    return any(TODO_MESSAGE.search(m) for m in messages)
                return True
            return any(NOT_IMPLEMENTED.search(m) for m in messages)
        if isinstance(s, ast.Pass) or (isinstance(s, ast.Expr) and isinstance(s.value, ast.Constant) and s.value.value is Ellipsis):
            return any(TODO.search(self.comments[n]) for n in range(fn.lineno, fn.end_lineno + 1) if n in self.comments)
        return False

    def commented_code(self):
        """Runs of comment lines that, without the #, parse as statements that do something."""
        spans = []
        run = []

        def flush():
            if not run:
                return
            text = "\n".join(t for _, t in run)
            if is_code(text):
                spans.append((run[0][0], run[-1][0]))
            else:
                for n, t in run:
                    if is_code(t):
                        spans.append((n, n))
            run.clear()

        last = None
        for n in sorted(self.comments):
            c = self.comments[n]
            text = c[1:]
            if text.startswith(" "):
                text = text[1:]
            if n in self.code or DIRECTIVE.match(c) or (n == 1 and c.startswith("#!")) or not text.strip() \
                    or text.lstrip().startswith((">>>", "...")):
                flush()
                last = None
                continue
            if last is not None and n != last + 1:
                flush()
            run.append((n, text))
            last = n
        flush()
        return spans

    # Tokens

    def tokens(self):
        """The token stream for duplication, after the imports, without comments, newlines or
        semicolons: each token's kind, name hash with the flags of facts.Tokens, and line, and the
        start position of each, for function spans."""
        skip, fixed = self.import_spans_and_keywords()
        locals_ = self.local_names if self.tree is not None else {}
        cols = ByteColumns(self.blines)
        kinds, names, lines, pos = [], [], [], []
        si = 0
        prev = None
        depth = 0  # inside an f-string (3.12's tokens), read as one literal
        lit = []
        for t in self.toks:
            if depth:
                lit.append(t.string)
                depth += (t.type in STRING_STARTS) - (t.type in STRING_ENDS)
                if not depth:
                    names[-1] = (fnv32("".join(lit).encode("utf-8", "replace")) & MASK) | LITERAL
                continue
            if t.type in SKIPPED or (t.type == token.OP and t.string == ";"):
                continue
            start = cols.start(t.start)
            while si < len(skip) and skip[si][1] <= start:
                si += 1
            if si < len(skip) and skip[si][0] <= start < skip[si][1]:
                continue
            kind, name = token_kind(t, prev, start not in fixed and start in locals_)
            if t.type in STRING_STARTS:
                depth, lit = 1, [t.string]
            kinds.append(kind)
            names.append(name)
            lines.append(t.start[0])
            pos.append(start)
            prev = t
        return kinds, names, lines, pos

    def import_spans_and_keywords(self):
        """The spans of the imports, sorted, which the stream leaves out, and the positions of
        keyword arguments, whose names a copy must keep."""
        skip, fixed = [], set()
        if self.tree is not None:
            for node in ast.walk(self.tree):
                if isinstance(node, (ast.Import, ast.ImportFrom)):
                    skip.append(((node.lineno, node.col_offset), (node.end_lineno, node.end_col_offset)))
                elif isinstance(node, ast.keyword) and node.arg is not None:
                    fixed.add((node.lineno, node.col_offset))
        skip.sort()
        return skip, fixed


class ByteColumns:
    """Turns tokenize's positions, whose columns count characters, into ast's, which count bytes of
    UTF-8. The byte offset of each character of the line being read is computed once per line: a
    long line of non-ASCII text would otherwise cost the length of the line for every token."""

    def __init__(self, blines):
        self.blines = blines
        self.row, self.offsets = 0, None

    def start(self, start):
        if start[0] != self.row:
            self.row, self.offsets = start[0], None
            line = self.blines[self.row - 1] if self.row <= len(self.blines) else b""
            if not line.isascii():
                self.offsets = [0]
                for ch in line.decode("utf-8", "replace"):
                    self.offsets.append(self.offsets[-1] + len(ch.encode("utf-8", "replace")))
        if self.offsets is not None and start[1] < len(self.offsets):
            return (start[0], self.offsets[start[1]])
        return start


def token_kind(t, prev, local):
    """A token's kind and name hash: a keyword, an identifier (local to its function unless it
    follows a dot, when local is true), a literal, an operator or indentation."""
    if t.type == token.NAME:
        if t.string in KEYWORDS:
            return KIND_KEYWORD + KEYWORDS[t.string], 0
        h = fnv32(t.string.encode("utf-8", "replace")) & MASK
        is_attr = prev is not None and prev.type == token.OP and prev.string == "."
        return KIND_IDENT, h | (LOCAL if local and not is_attr else FIXED)
    if t.type in (token.NUMBER, token.STRING):
        return KIND_LITERAL, (fnv32(t.string.encode("utf-8", "replace")) & MASK) | LITERAL
    if t.type in STRING_STARTS:
        return KIND_LITERAL, LITERAL  # the hash is set once the whole literal is read
    if t.type == token.OP:
        return KIND_OP + min(t.exact_type, 100), 0
    return {token.INDENT: KIND_INDENT, token.DEDENT: KIND_DEDENT}.get(t.type, KIND_OTHER), 0


LOCALS_CACHE = {}


def scope_locals(scope):
    """The names a function or lambda binds itself: its parameters and every name it assigns, unless
    declared global or nonlocal; names bound in nested functions belong to those."""
    key = id(scope)
    cached = LOCALS_CACHE.get(key)
    if cached is not None and cached[0] is scope:
        return cached[1]
    names, declared = set(), set()
    a = scope.args
    for arg in a.posonlyargs + a.args + a.kwonlyargs + [a.vararg, a.kwarg]:
        if arg is not None:
            names.add(arg.arg)
    stack = list(ast.iter_child_nodes(scope)) if isinstance(scope, ast.Lambda) else list(scope.body)
    while stack:
        node = stack.pop()
        if isinstance(node, (ast.Global, ast.Nonlocal)):
            declared.update(node.names)
        elif isinstance(node, ast.Name) and isinstance(node.ctx, (ast.Store, ast.Del)):
            names.add(node.id)
        elif isinstance(node, ast.ExceptHandler) and node.name:
            names.add(node.name)
        elif isinstance(node, ast.alias):
            names.add(node.asname or node.name.split(".")[0])
        elif isinstance(node, (ast.MatchAs, ast.MatchStar)) and node.name:
            names.add(node.name)
        elif isinstance(node, ast.MatchMapping) and node.rest:
            names.add(node.rest)
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            names.add(node.name)
            stack.extend(node.decorator_list)
            continue  # its body is its own scope
        if isinstance(node, ast.Lambda):
            continue
        stack.extend(ast.iter_child_nodes(node))
    names -= declared
    LOCALS_CACHE[key] = (scope, names)
    return names


def span(fn, pos):
    """The range of a function's tokens, from the one after its name to the end of its body."""
    import bisect

    # The tokens at the position of the def are "def" or "async", and the DEDENTs that close the
    # block before it; past them are "def" after "async", then the name.
    i = bisect.bisect_right(pos, (fn.lineno, fn.col_offset))
    i += 2 if isinstance(fn, ast.AsyncFunctionDef) else 1
    end = bisect.bisect_left(pos, (fn.end_lineno, fn.end_col_offset))
    i = min(i, len(pos))
    return [i, max(i, end)]


# Helpers for the AST


def without_docstring(body):
    if body and isinstance(body[0], ast.Expr) and isinstance(body[0].value, ast.Constant) \
            and isinstance(body[0].value.value, str):
        return body[1:]
    return body


def docstring_lines(fn):
    b = fn.body
    if b and isinstance(b[0], ast.Expr) and isinstance(b[0].value, ast.Constant) and isinstance(b[0].value.value, str):
        return set(range(b[0].lineno, b[0].end_lineno + 1))
    return set()


def dotted(e):
    if isinstance(e, ast.Name):
        return e.id
    if isinstance(e, ast.Attribute):
        return dotted(e.value) + "." + e.attr
    return ""


BROAD = {"Exception", "BaseException"}


def broad(t):
    """Whether an except clause catches every exception: bare, Exception or BaseException."""
    if t is None:
        return True
    if isinstance(t, ast.Tuple):
        return any(broad(e) for e in t.elts)
    return dotted(t).split(".")[-1] in BROAD


def catches_import_error(t):
    if t is None:
        return True
    if isinstance(t, ast.Tuple):
        return any(catches_import_error(e) for e in t.elts)
    return dotted(t).split(".")[-1] in BROAD | {"ImportError", "ModuleNotFoundError"}


def ignores(body):
    """Whether a handler's body does nothing: only pass, ... or continue."""
    for s in body:
        if isinstance(s, (ast.Pass, ast.Continue)):
            continue
        if isinstance(s, ast.Expr) and isinstance(s.value, ast.Constant) and s.value.value is Ellipsis:
            continue
        return False
    return True


NOT_IMPLEMENTED = re.compile(r"(?i)\b(not (yet )?implemented|unimplemented|implement me)\b|^\s*todo\s*($|[:.!-])")
TODO = re.compile(r"(?i)^#\s*(todo|fixme)\b")
TODO_MESSAGE = re.compile(r"(?i)^\s*todo\s*($|[:.!-])")
DIRECTIVE = re.compile(
    r"#\s*(type:|noqa|pylint:|fmt:|pragma|isort:|mypy:|pyright:|ruff:|flake8|nosec|coding[:=]|-\*-|%%|:)")


def is_code(text):
    """Whether text parses as Python of which one statement does something."""
    import textwrap

    try:
        tree = ast.parse(textwrap.dedent(text))
    except (SyntaxError, ValueError, RecursionError, MemoryError):
        return False
    return any(does_something(s, text) for s in tree.body)


def does_something(s, text):
    if isinstance(s, (ast.Assign, ast.AugAssign)):
        targets = s.targets if isinstance(s, ast.Assign) else [s.target]
        return all(assignable(t) for t in targets)
    if isinstance(s, ast.AnnAssign):
        # "flags: --line-length=88" and "Note: x = 1" parse too: an annotation is a type, and a
        # capitalised word before the colon is a label of prose (constants are in capitals).
        label = isinstance(s.target, ast.Name) and s.target.id[:1].isupper() and not s.target.id.isupper()
        return s.value is not None and not label and is_type(s.annotation)
    if isinstance(s, ast.Expr):
        v = s.value
        if isinstance(v, ast.Call):
            # Written as code is written: no space before the parenthesis ("see (2)" is prose).
            return call_tight(v, text)
        return isinstance(v, (ast.Await, ast.Yield, ast.YieldFrom))
    return isinstance(s, (ast.Return, ast.Raise, ast.Delete, ast.Import, ast.ImportFrom, ast.If, ast.For,
                          ast.AsyncFor, ast.While, ast.With, ast.AsyncWith, ast.Try, ast.FunctionDef,
                          ast.AsyncFunctionDef, ast.ClassDef, ast.Assert, ast.Match))


def is_type(e):
    """Whether an annotation looks like a type: a name, dotted name, subscript (list[int]), string, None,
    or a union of those."""
    if isinstance(e, ast.BinOp):
        return isinstance(e.op, ast.BitOr) and is_type(e.left) and is_type(e.right)
    if isinstance(e, ast.Constant):
        return e.value is None or isinstance(e.value, str)
    return isinstance(e, (ast.Name, ast.Attribute, ast.Subscript))


def assignable(t):
    if isinstance(t, (ast.Tuple, ast.List)):
        return all(assignable(e) for e in t.elts)
    return isinstance(t, (ast.Name, ast.Attribute, ast.Subscript, ast.Starred))


def call_tight(call, text):
    import textwrap

    lines = textwrap.dedent(text).split("\n")
    f = call.func
    if f.end_lineno is None or f.end_lineno > len(lines):
        return False
    line = lines[f.end_lineno - 1].encode("utf-8")
    return line[f.end_col_offset:f.end_col_offset + 1] == b"("


# Metrics


def cognitive(fn, src_lines):
    """Cognitive complexity as SonarSource's Python analyzer computes it (sonar-python
    CognitiveComplexityVisitor, from G. Ann Campbell's paper): +1 for if, elif, else, for, while,
    except and each run of like boolean operators, plus the nesting level for if, for, while, except
    and conditional expressions. Nested functions count toward the function they are in; recursion is
    not counted."""
    c = Cognitive(src_lines)
    c.function(fn, 0)
    return c.total


class Cognitive:
    def __init__(self, src_lines):
        self.lines = src_lines
        self.total = 0
        self.frames = []  # the functions and classes the visit is in, innermost last

    def function(self, fn, level):
        self.frames.append(fn)
        for d in fn.decorator_list:
            self.expr(d, level)
        self.arguments(fn.args, level)
        if fn.returns is not None:
            self.expr(fn.returns, level)
        self.block(fn.body, level)
        self.frames.pop()

    def arguments(self, a, level):
        for arg in a.posonlyargs + a.args + a.kwonlyargs + [a.vararg, a.kwarg]:
            if arg is not None and arg.annotation is not None:
                self.expr(arg.annotation, level)
        for d in a.defaults + [d for d in a.kw_defaults if d is not None]:
            self.expr(d, level)

    def block(self, body, level):
        for s in body:
            self.stmt(s, level)

    def stmt(self, s, level):
        for types, visit in self.STATEMENTS:
            if isinstance(s, types):
                visit(self, s, level)
                return
        self.children(s, level)

    def if_(self, s, level):
        self.total += 1 + level
        self.expr(s.test, level)
        self.block(s.body, level + 1)
        orelse = s.orelse
        while len(orelse) == 1 and isinstance(orelse[0], ast.If) and self.is_elif(orelse[0]):
            e = orelse[0]
            self.total += 1
            self.expr(e.test, level)
            self.block(e.body, level + 1)
            orelse = e.orelse
        self.else_(orelse, level)

    def else_(self, orelse, level):
        if orelse:
            self.total += 1
            self.block(orelse, level + 1)

    def loop(self, s, level):
        self.total += 1 + level
        if isinstance(s, ast.While):
            self.expr(s.test, level)
        else:
            self.expr(s.target, level)
            self.expr(s.iter, level)
        self.block(s.body, level + 1)
        self.else_(s.orelse, level)

    def try_(self, s, level):
        self.block(s.body, level)
        for h in s.handlers:
            self.total += 1 + level
            self.expr(h.type, level)
            self.block(h.body, level + 1)
        self.else_(s.orelse, level)
        self.block(s.finalbody, level)

    def with_(self, s, level):
        for item in s.items:
            self.expr(item.context_expr, level)
        self.block(s.body, level)

    def match(self, s, level):
        self.expr(s.subject, level)
        for case in s.cases:
            self.expr(case.guard, level)
            self.block(case.body, level + 1)

    def nested_function(self, s, level):
        """A function in a function starts one level below the code around it, unless the outer
        function only returns it (a decorator's wrapper); a method starts at 0."""
        outer = self.frames[-1] if self.frames else None
        if not isinstance(outer, FUNCS):
            self.function(s, 0)
        elif all(o is s or simple_return(o) for o in outer.body):
            self.function(s, level)
        else:
            self.function(s, level + 1)

    def class_(self, s, level):
        for e in s.decorator_list + s.bases + [k.value for k in s.keywords]:
            self.expr(e, level)
        self.frames.append(s)
        self.block(s.body, 0)
        self.frames.pop()

    def children(self, node, level):
        """Visits the children of a statement that neither adds nor nests, at its level."""
        for child in ast.iter_child_nodes(node):
            if isinstance(child, ast.expr):
                self.expr(child, level)
            elif isinstance(child, ast.stmt):
                self.stmt(child, level)
            else:
                for gc in ast.iter_child_nodes(child):
                    if isinstance(gc, ast.expr):
                        self.expr(gc, level)

    STATEMENTS = (
        (ast.If, if_),
        ((ast.For, ast.AsyncFor, ast.While), loop),
        ((ast.Try, getattr(ast, "TryStar", ast.Try)), try_),
        ((ast.With, ast.AsyncWith), with_),
        (getattr(ast, "Match", ()), match),  # 3.10; the helper still loads on 3.9 to say it is too old
        ((ast.FunctionDef, ast.AsyncFunctionDef), nested_function),
        (ast.ClassDef, class_),
    )

    def is_elif(self, node):
        return is_elif(self.lines, node)

    def expr(self, e, level):
        if e is None:
            return
        if isinstance(e, ast.BoolOp):
            self.bool_op(e, level)
            return
        if isinstance(e, ast.IfExp):
            self.total += 1 + level
            for child in (e.test, e.body, e.orelse):
                self.expr(child, level + 1)
            return
        for child in ast.iter_child_nodes(e):
            if isinstance(child, ast.expr):
                self.expr(child, level)
            elif isinstance(child, (ast.comprehension, ast.keyword, ast.arguments)):
                for gc in ast.iter_child_nodes(child):
                    if isinstance(gc, ast.expr):
                        self.expr(gc, level)

    def bool_op(self, e, level):
        """A chain of and/or adds 1 for each run of one operator."""
        ops = []
        self.flatten(e, ops, level)
        prev = None
        for op in ops:
            if op is not prev:
                self.total += 1
            prev = op

    def flatten(self, e, ops, level):
        """The operators of a chain of and/or, left to right, through operands that are themselves
        unparenthesised and/or chains; other operands are visited on their own."""
        op = type(e.op)
        for i, v in enumerate(e.values):
            if i:
                ops.append(op)
            if isinstance(v, ast.BoolOp) and not self.parenthesised(v):
                self.flatten(v, ops, level)
            else:
                self.expr(v, level)

    def parenthesised(self, node):
        line = self.lines[node.lineno - 1] if node.lineno <= len(self.lines) else b""
        i = node.col_offset - 1
        while i >= 0 and line[i:i + 1] in (b" ", b"\t"):
            i -= 1
        return i >= 0 and line[i:i + 1] == b"("


def simple_return(s):
    return isinstance(s, ast.Return) and isinstance(s.value, ast.Name)


def cyclomatic(fn):
    """McCabe's cyclomatic complexity as radon counts it (radon.visitors.ComplexityVisitor): 1, plus
    1 for each if, elif and conditional expression, loop (and its else), except clause (and try
    else), boolean operator after the first, comprehension and its ifs, match case other than _, and
    assert, whose condition radon does not look into. Nested functions and classes are measured on
    their own and add nothing."""
    total = 1
    stack = list(fn.body)
    while stack:
        node = stack.pop()
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            continue
        if isinstance(node, (ast.Try, getattr(ast, "TryStar", ast.Try))):
            total += len(node.handlers) + bool(node.orelse)
        elif isinstance(node, ast.BoolOp):
            total += len(node.values) - 1
        elif isinstance(node, (ast.If, ast.IfExp)):
            total += 1
        elif isinstance(node, ast.Match):
            wildcard = any(getattr(c.pattern, "pattern", False) is None for c in node.cases)
            total += max(0, len(node.cases) - wildcard)
        elif isinstance(node, (ast.For, ast.While, ast.AsyncFor)):
            total += bool(node.orelse) + 1
        elif isinstance(node, ast.comprehension):
            total += len(node.ifs) + 1
        elif isinstance(node, ast.Assert):
            total += 1
            continue  # radon does not look inside an assert
        stack.extend(ast.iter_child_nodes(node))
    return total


NESTS = (ast.If, ast.For, ast.AsyncFor, ast.While, ast.Try, ast.With, ast.AsyncWith)


def nesting(body, d, lines):
    """The deepest nesting of if, for, while, try and with statements, as SonarSource's python:S134
    counts it: an elif, else or except is part of its statement, match adds nothing, and a nested
    function continues the count."""
    deepest = d
    for s in body:
        deepest = max(deepest, nest_of(s, d, lines))
    return deepest


def nest_of(s, d, lines):
    if isinstance(s, ast.If):
        deepest = max(d + 1, nesting(s.body, d + 1, lines))
        orelse = s.orelse
        # An elif continues the if; an if alone in an else block is nested in it.
        while len(orelse) == 1 and isinstance(orelse[0], ast.If) and is_elif(lines, orelse[0]):
            deepest = max(deepest, nesting(orelse[0].body, d + 1, lines))
            orelse = orelse[0].orelse
        return max(deepest, nesting(orelse, d + 1, lines))
    if isinstance(s, NESTS) or isinstance(s, getattr(ast, "TryStar", ast.Try)):
        deepest = d + 1
        for field in ("body", "orelse", "finalbody"):
            deepest = max(deepest, nesting(getattr(s, field, []), d + 1, lines))
        for h in getattr(s, "handlers", []):
            deepest = max(deepest, nesting(h.body, d + 1, lines))
        for c in getattr(s, "cases", []):
            deepest = max(deepest, nesting(c.body, d + 1, lines))
        return deepest
    if isinstance(s, ast.Match):
        return max([d] + [nesting(c.body, d, lines) for c in s.cases])
    if isinstance(s, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
        return nesting(s.body, d, lines)
    return d


def is_elif(lines, node):
    """Whether an If node of an orelse was written elif, which ast does not record. lines are the
    source's lines as bytes, as col_offset counts."""
    line = lines[node.lineno - 1] if node.lineno <= len(lines) else b""
    return line[node.col_offset:node.col_offset + 4] == b"elif"


main()
