package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// githealthcheck has no module dependencies outside the standard library (ADR 0003), so reports are
// checked against schema/report.v1.json by the small validator below instead of a JSON Schema
// library. It knows only the keywords the schema uses, and fails on any other, so a keyword added to
// the schema later cannot be skipped silently.

var (
	reportSchema     map[string]any
	reportSchemaErr  error
	reportSchemaOnce sync.Once
)

func loadReportSchema(t *testing.T) map[string]any {
	t.Helper()
	reportSchemaOnce.Do(func() {
		var raw []byte
		raw, reportSchemaErr = os.ReadFile(filepath.Join("..", "..", "schema", "report.v1.json"))
		if reportSchemaErr == nil {
			reportSchemaErr = json.Unmarshal(raw, &reportSchema)
		}
	})
	if reportSchemaErr != nil {
		t.Fatalf("schema/report.v1.json: %v", reportSchemaErr)
	}
	return reportSchema
}

// validateReport fails the test when a JSON report does not match schema/report.v1.json.
func validateReport(t *testing.T, report []byte) {
	t.Helper()
	if errs := schemaErrors(t, report); len(errs) > 0 {
		t.Errorf("report does not match schema/report.v1.json:\n  %s\n%s", strings.Join(errs, "\n  "), report)
	}
}

func schemaErrors(t *testing.T, report []byte) []string {
	t.Helper()
	root := loadReportSchema(t)
	dec := json.NewDecoder(bytes.NewReader(report))
	dec.UseNumber() // keeps 3 and 3.5 apart for "integer"
	var inst any
	if err := dec.Decode(&inst); err != nil {
		return []string{err.Error()}
	}
	return checkSchema(root, root, inst, "")
}

var schemaAnnotations = map[string]bool{"$schema": true, "$id": true, "$defs": true, "title": true, "description": true}

// checkSchema returns where inst breaks schema, as "<JSON pointer>: <problem>".
func checkSchema(root, schema map[string]any, inst any, at string) []string {
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, at+": "+fmt.Sprintf(format, args...)) }
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	obj, isObj := inst.(map[string]any)
	for _, k := range keys {
		v := schema[k]
		switch k {
		case "$ref":
			name, ok := strings.CutPrefix(v.(string), "#/$defs/")
			def, found := root["$defs"].(map[string]any)[name].(map[string]any)
			if !ok || !found {
				fail("unresolvable $ref %v", v)
				continue
			}
			errs = append(errs, checkSchema(root, def, inst, at)...)
		case "type":
			types, ok := v.([]any)
			if !ok {
				types = []any{v}
			}
			if !slices.ContainsFunc(types, func(t any) bool { return hasType(inst, t.(string)) }) {
				fail("%s is not of type %v", short(inst), v)
			}
		case "const":
			if !sameJSON(inst, v) {
				fail("%s, want %v", short(inst), v)
			}
		case "enum":
			if !slices.ContainsFunc(v.([]any), func(e any) bool { return sameJSON(inst, e) }) {
				fail("%s is not one of the enum", short(inst))
			}
		case "required":
			for _, name := range v.([]any) {
				if _, ok := obj[name.(string)]; isObj && !ok {
					fail("missing %q", name)
				}
			}
		case "properties":
			for name, sub := range v.(map[string]any) {
				if val, ok := obj[name]; isObj && ok {
					errs = append(errs, checkSchema(root, sub.(map[string]any), val, at+"/"+name)...)
				}
			}
		case "additionalProperties":
			props, _ := schema["properties"].(map[string]any)
			for name, val := range obj {
				if _, declared := props[name]; declared {
					continue
				}
				switch ap := v.(type) {
				case bool:
					if !ap {
						fail("unknown field %q", name)
					}
				case map[string]any:
					errs = append(errs, checkSchema(root, ap, val, at+"/"+name)...)
				}
			}
		case "propertyNames":
			for name := range obj {
				errs = append(errs, checkSchema(root, v.(map[string]any), name, at+"/"+name+" (name)")...)
			}
		case "items":
			if arr, ok := inst.([]any); ok {
				for i, val := range arr {
					errs = append(errs, checkSchema(root, v.(map[string]any), val, at+"/"+strconv.Itoa(i))...)
				}
			}
		case "oneOf":
			matched := 0
			for _, sub := range v.([]any) {
				if len(checkSchema(root, sub.(map[string]any), inst, at)) == 0 {
					matched++
				}
			}
			if matched != 1 {
				fail("%s matches %d of the oneOf schemas, want exactly 1", short(inst), matched)
			}
		case "minimum", "maximum":
			n, ok := inst.(json.Number)
			if !ok {
				continue
			}
			x, _ := n.Float64()
			if limit := v.(float64); (k == "minimum" && x < limit) || (k == "maximum" && x > limit) {
				fail("%s is outside %s %v", n, k, limit)
			}
		case "pattern":
			if s, ok := inst.(string); ok && !regexp.MustCompile(v.(string)).MatchString(s) {
				fail("%q does not match %s", s, v)
			}
		default:
			if !schemaAnnotations[k] {
				fail("the test's validator does not know the keyword %q; teach it before using it", k)
			}
		}
	}
	return errs
}

func hasType(inst any, typ string) bool {
	switch typ {
	case "object":
		_, ok := inst.(map[string]any)
		return ok
	case "array":
		_, ok := inst.([]any)
		return ok
	case "string":
		_, ok := inst.(string)
		return ok
	case "boolean":
		_, ok := inst.(bool)
		return ok
	case "null":
		return inst == nil
	case "number":
		_, ok := inst.(json.Number)
		return ok
	case "integer":
		n, ok := inst.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Int64()
		return err == nil
	}
	return false
}

// sameJSON compares a decoded report value with a value from the schema, which was decoded without
// UseNumber.
func sameJSON(inst, schemaValue any) bool {
	if n, ok := inst.(json.Number); ok {
		x, err := n.Float64()
		f, isNum := schemaValue.(float64)
		return err == nil && isNum && x == f
	}
	return inst == schemaValue
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 60 {
		return string(b[:60]) + "..."
	}
	return string(b)
}

// Every constant of the report's enum types is in the schema, and nothing else is: a reason added
// in Go and not in the schema would make valid reports fail a consumer's validation.
func TestSchemaEnumsMatchTheGoConstants(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "internal", "report", "report.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string][]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			typ, ok := vs.Type.(*ast.Ident)
			if !ok {
				continue
			}
			for _, val := range vs.Values {
				if lit, ok := val.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					consts[typ.Name] = append(consts[typ.Name], s)
				}
			}
		}
	}
	defs := loadReportSchema(t)["$defs"].(map[string]any)
	for goType, def := range map[string]string{"NullReason": "nullReason", "SkipReason": "skipReason", "Family": "family"} {
		var inSchema []string
		for _, e := range defs[def].(map[string]any)["enum"].([]any) {
			inSchema = append(inSchema, e.(string))
		}
		inGo := consts[goType]
		if len(inGo) == 0 {
			t.Fatalf("found no %s constants in internal/report/report.go", goType)
		}
		slices.Sort(inGo)
		slices.Sort(inSchema)
		if !slices.Equal(inGo, inSchema) {
			t.Errorf("%s constants %v, schema $defs/%s enum %v", goType, inGo, def, inSchema)
		}
	}
}

// The validator must catch what the schema forbids; otherwise every report would pass.
func TestSchemaRejectsBrokenReports(t *testing.T) {
	r, _, _ := scanJSON(t, gitRepo(t))
	good, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if errs := schemaErrors(t, good); len(errs) > 0 {
		t.Fatalf("a real report fails: %v", errs)
	}
	for name, tc := range map[string]struct{ old, new, want string }{
		"unknown field":    {`"shallow":`, `"branch":"main","shallow":`, `unknown field "branch"`},
		"unknown reason":   {`"skipped":[]`, `"skipped":[{"path":"a","reason":"too_old"}]`, "/skipped/0/reason"},
		"missing field":    {`"walkComplete":true,`, ``, `missing "walkComplete"`},
		"wrong version":    {`"schemaVersion":1`, `"schemaVersion":2`, "/schemaVersion"},
		"fractional count": {`"filesTotal":`, `"filesTotal":1.5,"x":`, "/inputs/filesTotal"},
	} {
		if !strings.Contains(string(good), tc.old) {
			t.Fatalf("%s: the report has no %s to change:\n%s", name, tc.old, good)
		}
		broken := strings.Replace(string(good), tc.old, tc.new, 1)
		errs := strings.Join(schemaErrors(t, []byte(broken)), "\n")
		if !strings.Contains(errs, tc.want) {
			t.Errorf("%s: errors %q, want one about %q", name, errs, tc.want)
		}
	}
	// A measurement is a value or a null with a reason, never both and never neither.
	for name, score := range map[string]map[string]any{
		"value and reason": {"value": 5.2, "reason": "timeout", "inputs": 3},
		"null, no reason":  {"value": nil, "inputs": 0},
		"no value":         {"reason": "timeout", "inputs": 0},
	} {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		m["overall"].(map[string]any)["score"] = score
		broken, _ := json.Marshal(m)
		if errs := strings.Join(schemaErrors(t, broken), "\n"); !strings.Contains(errs, "/overall/score: ") || !strings.Contains(errs, "oneOf") {
			t.Errorf("%s: errors %q, want a oneOf error at /overall/score", name, errs)
		}
	}
}
