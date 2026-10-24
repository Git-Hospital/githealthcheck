package gitx

import "strings"

// Mailmap maps the names and emails commits were made with to a person's canonical ones, as git's
// .mailmap does (https://git-scm.com/docs/gitmailmap, mailmap.c). Emails and names match ignoring
// ASCII case, as git's strcasecmp does.
type Mailmap map[string]*mailmapEntry // by lower-cased commit email

type mailmapEntry struct {
	byName map[string]*identity // by lower-cased commit name
	any    *identity            // the entry that matches any name
}

type identity struct{ name, email string }

// ParseMailmap parses a .mailmap. Lines it cannot read are skipped, as git skips them; only a line
// that starts with # is a comment.
func ParseMailmap(src []byte) Mailmap {
	m := Mailmap{}
	for line := range strings.SplitSeq(string(src), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		name1, email1, rest, ok := nameAndEmail(line)
		if !ok || email1 == "" {
			continue // the proper email may not be empty
		}
		if name2, email2, _, ok := nameAndEmail(rest); ok {
			m.add(email2, name2, name1, email1)
		} else {
			m.add(email1, "", name1, "")
		}
	}
	return m
}

// nameAndEmail reads "Name <email>" from the start of s and returns what follows.
func nameAndEmail(s string) (name, email, rest string, ok bool) {
	l := strings.IndexByte(s, '<')
	if l < 0 {
		return "", "", "", false
	}
	r := strings.IndexByte(s[l:], '>')
	if r < 0 {
		return "", "", "", false
	}
	return strings.Trim(s[:l], " \t\n\v\f\r"), s[l+1 : l+r], s[l+r+1:], true
}

// add records a mapping. Lines for the same commit email alone merge, so one can give the proper name
// and another the proper email; a later line for the same email and name replaces the earlier one.
func (m Mailmap) add(oldEmail, oldName, newName, newEmail string) {
	key := asciiLower(oldEmail)
	e := m[key]
	if e == nil {
		e = &mailmapEntry{byName: map[string]*identity{}}
		m[key] = e
	}
	if oldName != "" {
		e.byName[asciiLower(oldName)] = &identity{name: newName, email: newEmail}
		return
	}
	if e.any == nil {
		e.any = &identity{}
	}
	if newName != "" {
		e.any.name = newName
	}
	if newEmail != "" {
		e.any.email = newEmail
	}
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// Map returns the canonical name and email of a commit's author. An entry for the exact name wins
// over one for the email alone.
func (m Mailmap) Map(name, email string) (string, string) {
	e := m[asciiLower(email)]
	if e == nil {
		return name, email
	}
	id := e.byName[asciiLower(name)]
	if id == nil {
		id = e.any
	}
	if id == nil {
		return name, email
	}
	if id.name != "" {
		name = id.name
	}
	if id.email != "" {
		email = id.email
	}
	return name, email
}
