package tests

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func FuzzParseCobertura(f *testing.F) {
	fixture, err := os.ReadFile("testdata/coverage.py.xml")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(fixture)
	for _, seed := range []string{
		"",
		"<coverage/>",
		"<coverage><unclosed>",
		`<report><class filename="a.py"/></report>`,
		`<coverage><class><lines/></class></coverage>`,
		`<!DOCTYPE coverage [<!ENTITY x "a">]><coverage><sources><source>&x;</source></sources></coverage>`,
		`<!DOCTYPE coverage [<!ENTITY x SYSTEM "file:///synthetic/missing">]><coverage><sources><source>&x;</source></sources></coverage>`,
		`<coverage><sources><source>C:\work\src</source></sources><class filename="pkg\mod.py"><lines><line number="1" hits="0"/><line number="1" hits="2"/></lines></class></coverage>`,
		`<coverage><class filename="a.py"><lines><line number="0" hits="1"/></lines></class></coverage>`,
		`<coverage><class filename="a.py"><lines><line number="x" hits="1"/></lines></class></coverage>`,
		`<coverage><class filename="a.py"><lines><line number="1" hits="-1"/></lines></class></coverage>`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		lookups := 0
		p, err := parseCobertura(context.Background(), data, func(full string) (string, bool) {
			lookups++
			return full, true
		})
		if lookups > maxLookups {
			t.Fatalf("%d resolver calls exceed the lookup budget", lookups)
		}
		if err != nil {
			if p != nil {
				t.Fatal("failed parse returned a partial coverage profile")
			}
			return
		}
		if p == nil || p.mode != "set" || p.files < 0 || p.inModules < len(p.blocks) {
			t.Fatalf("invalid profile: %+v", p)
		}
		// A broken trailing tag must discard even an otherwise valid report.
		broken := append(append([]byte(nil), data...), '<')
		partial, parseErr := parseCobertura(context.Background(), broken, func(full string) (string, bool) { return full, true })
		if parseErr == nil || partial != nil {
			t.Fatal("malformed XML returned coverage")
		}
		for _, lines := range p.blocks {
			for k, b := range lines {
				if k.startLine < 1 || k.startLine > maxLine || k.endLine != k.startLine || k.startCol != 0 || k.endCol != 0 || b == nil || b.stmts != 1 {
					t.Fatalf("invalid line block: %+v, %+v", k, b)
				}
			}
		}
	})
}

// Keep the XML well formed while mutating lengths and counts, so fuzzing reaches
// the resource limits instead of stopping at the first XML syntax error.
func FuzzParseCoberturaLimits(f *testing.F) {
	f.Add(uint8(1), uint16(4), uint16(4), uint8(0), uint32(1))
	f.Add(uint8(maxSources), uint16(maxNameBytes), uint16(maxNameBytes), uint8(maxDepth-4), uint32(maxLine))
	f.Add(uint8(maxSources+1), uint16(4), uint16(4), uint8(0), uint32(1))
	f.Add(uint8(1), uint16(maxNameBytes+1), uint16(4), uint8(0), uint32(1))
	f.Add(uint8(1), uint16(4), uint16(maxNameBytes+1), uint8(0), uint32(1))
	f.Add(uint8(1), uint16(4), uint16(4), uint8(maxDepth-3), uint32(1))
	f.Add(uint8(1), uint16(4), uint16(4), uint8(0), uint32(maxLine+1))
	f.Add(uint8(1), uint16(4), uint16(4), uint8(0), uint32(0))
	f.Fuzz(func(t *testing.T, sourceCount uint8, sourceBytes, nameBytes uint16, nesting uint8, line uint32) {
		count := int(sourceCount) % (maxSources + 2)
		size := int(sourceBytes) % (maxNameBytes + 2)
		name := int(nameBytes) % (maxNameBytes + 2)
		depth := int(nesting) % maxDepth
		line %= maxLine + 2
		xml := "<coverage><sources>" + strings.Repeat("<source>"+strings.Repeat("s", size)+"</source>", count) + "</sources>" +
			strings.Repeat("<a>", depth) + fmt.Sprintf(`<class filename="%s"><lines><line number="%d" hits="1"/></lines></class>`, strings.Repeat("f", name), line) +
			strings.Repeat("</a>", depth) + "</coverage>"
		p, err := parseCobertura(context.Background(), []byte(xml), func(full string) (string, bool) { return full, true })
		invalid := count > maxSources || (count > 0 && size > maxNameBytes) || name == 0 || name > maxNameBytes || depth+4 > maxDepth || line == 0 || line > maxLine
		if invalid {
			if err == nil || p != nil {
				t.Fatalf("over-bound report returned coverage: profile=%+v err=%v", p, err)
			}
			return
		}
		if err != nil || p == nil {
			t.Fatalf("within-bound report rejected: %v", err)
		}
		if p.files != 1 || len(p.blocks) != 1 {
			t.Fatalf("expected one file, got %+v", p)
		}
		for _, lines := range p.blocks {
			b := lines[blockKey{int(line), 0, int(line), 0}]
			if len(lines) != 1 || b == nil || b.stmts != 1 || !b.covered {
				t.Fatalf("expected the supplied covered line, got %+v", lines)
			}
		}
	})
}
