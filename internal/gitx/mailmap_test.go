package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The .mailmap forms of gitmailmap(5), plus the corners: comments, case, repeated lines, blank and
// malformed entries. Each identity below is mapped by git check-mailmap and by Mailmap, and the two
// must agree.
const mailmapSource = `# a comment <c@example.com>
Proper Name <commit@example.com>
<proper@example.com> <only-email@example.com>
Both Fixed <both@example.com> <old@example.com>
Named Only <named@example.com> Old Name <shared@example.com>
Other Person <other@example.com> Second Name <shared@example.com>
Simple Shared <shared@example.com>
Upper Case <MIXED@Example.com>
First Line <twice@example.com>
<twice-proper@example.com> <twice@example.com>
Repeated <rep1@example.com> Rep <rep@example.com>
Repeated Again <rep2@example.com> Rep <rep@example.com>
  Spaced   Name   <spaced@example.com>
Tabbed	Name	<tab@example.com>	<tabbed-old@example.com>
Empty Email <> <empty-old@example.com>
<> <no-name-empty@example.com>
No Close <broken@example.com
No Open broken@example.com>
Trailing <trail@example.com> # not a comment
 # indented <indent@example.com>
Unicode Ünal <unicode@example.com>
Named First <nf@example.com> NF <nf-old@example.com>
<nf-second@example.com> NF <nf-old@example.com>
` + "\u00a0NBSP Name\u00a0 <nbsp@example.com>" + `
Umlaut <ÜMLAUT@example.com>
Name Case <name-case@example.com> ÜLKE <name-case-old@example.com>
CRLF Name <crlf@example.com>` + "\r\n" + `Last Line <last@example.com>`

var mailmapIdentities = []string{
	"Someone <commit@example.com>",
	"Someone <COMMIT@EXAMPLE.COM>",
	"Someone <only-email@example.com>",
	"Someone <old@example.com>",
	"Old Name <shared@example.com>",
	"old name <shared@example.com>",
	"Second Name <shared@example.com>",
	"Third Name <shared@example.com>",
	"Someone <mixed@example.com>",
	"Someone <twice@example.com>",
	"Rep <rep@example.com>",
	"Someone <spaced@example.com>",
	"Someone <tabbed-old@example.com>",
	"Someone <empty-old@example.com>",
	"Someone <no-name-empty@example.com>",
	"Someone <broken@example.com>",
	"Someone <trail@example.com>",
	"Someone <indent@example.com>",
	"Someone <unicode@example.com>",
	"Someone <crlf@example.com>",
	"Someone <last@example.com>",
	"Someone <unmapped@example.com>",
	"Someone <c@example.com>",
	"NF <nf-old@example.com>",
	"Someone <nbsp@example.com>",
	"Someone <ümlaut@example.com>",
	"Someone <ÜMLAUT@EXAMPLE.COM>",
	"ülke <name-case-old@example.com>",
	"ÜLKE <name-case-old@example.com>",
}

func TestMailmapMatchesGit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mailmap")
	if err := os.WriteFile(file, []byte(mailmapSource), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1")
		return cmd.Output()
	}
	if _, err := git("init", "-q"); err != nil {
		t.Fatal(err)
	}
	m := ParseMailmap([]byte(mailmapSource))
	for _, id := range mailmapIdentities {
		out, err := git("-c", "mailmap.file="+file, "check-mailmap", id)
		if err != nil {
			t.Fatalf("git check-mailmap %q: %v", id, err)
		}
		want := strings.TrimSuffix(string(out), "\n")
		name, email, _ := strings.Cut(strings.TrimSuffix(id, ">"), " <")
		gotName, gotEmail := m.Map(name, email)
		got := gotName + " <" + gotEmail + ">"
		if gotName == "" {
			got = "<" + gotEmail + ">"
		}
		if got != want {
			t.Errorf("%s: got %q, git says %q", id, got, want)
		}
	}
}
