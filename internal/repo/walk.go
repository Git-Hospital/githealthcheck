// Package repo reads the files of one commit from a repository's work tree and classifies every source
// file. Only paths in the commit's tree are read, so the counts belong to the commit the report names,
// not to untracked or ignored files. It treats both the tree and the work tree as untrusted: symlinks
// are never followed, oversized files are never read, and no path can leave the root.
package repo

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// MaxSourceBytes is the largest source file that is read, and MaxReportBytes the largest coverage
// report (architecture §2).
const (
	MaxSourceBytes = 1 << 20
	MaxReportBytes = 50 << 20
)

// Tree is the result of a walk.
type Tree struct {
	Root    string
	Files   []facts.File
	Skipped []report.Skip
}

// Entry is one path in a commit's tree, as git ls-tree prints it.
type Entry struct {
	Mode string // 100644, 100755, 120000 (symlink) or 160000 (submodule)
	OID  string
	Path string
}

// ParseTree parses the output of git ls-tree -r -z --full-tree.
func ParseTree(out []byte) ([]Entry, error) {
	var entries []Entry
	for rec := range bytes.SplitSeq(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, p, ok := bytes.Cut(rec, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		entries = append(entries, Entry{Mode: fields[0], OID: fields[2], Path: string(p)})
	}
	return entries, nil
}

// BlobID returns git's object name function for the object format ("sha1" or "sha256"), applied to a
// blob's content.
func BlobID(format string) (func([]byte) string, error) {
	var h func() hash.Hash
	switch format {
	case "sha1":
		h = sha1.New
	case "sha256":
		h = sha256.New
	default:
		return nil, fmt.Errorf("unknown object format %q", format)
	}
	return func(b []byte) string {
		d := h()
		fmt.Fprintf(d, "blob %d\x00", len(b))
		d.Write(b)
		return hex.EncodeToString(d.Sum(nil))
	}, nil
}

// vendorDirs are directory names whose contents are third-party code.
var vendorDirs = map[string]bool{"vendor": true, "node_modules": true, "third_party": true, "bower_components": true}

// validPath rejects what a crafted tree object could carry: empty, . or .. components, and anything
// inside a .git directory in any letter case.
func validPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

// Walk reads the commit's files in entries from the work tree under root. blobID names a blob's
// content in the repository's object format; a file whose content does not match its entry is marked
// Modified. Paths in the tree but not on disk are skipped as missing.
//
// Everything is opened through an os.Root, which refuses any path that leaves root, even through a
// directory swapped for a symlink.
func Walk(ctx context.Context, root string, entries []Entry, blobID func([]byte) string) (*Tree, error) {
	t := &Tree{Root: root}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for _, e := range entries {
		if ctx.Err() != nil {
			// The files read so far are still returned, so a timed-out run can report what it did
			// measure.
			return t, ctx.Err()
		}
		skip := func(reason report.SkipReason, err error) {
			s := report.Skip{Path: e.Path, Reason: reason}
			if err != nil {
				s.Detail = cause(err)
			}
			t.Skipped = append(t.Skipped, s)
		}
		switch {
		case !validPath(e.Path):
			// Not echoed back: the name itself is the attack.
			t.Skipped = append(t.Skipped, report.Skip{Path: "", Reason: report.SkipInvalidPath})
			continue
		case e.Mode == "160000":
			skip(report.SkipNestedRepo, nil)
			continue
		}
		name := language(e.Path)
		if name == "" {
			continue
		}
		if e.Mode == "120000" {
			skip(report.SkipSymlink, nil)
			continue
		}
		src, size, reason, err := readEntry(r, e.Path, MaxSourceBytes)
		if reason != "" {
			skip(reason, err)
			continue
		}
		f := facts.File{
			Path:     e.Path,
			Language: name,
			Size:     size,
			Lines:    countLines(src),
			Vendored: vendored(e.Path),
			Modified: blobID(src) != e.OID,
		}
		if s := lang.For(name); s != nil {
			s.Fill(&f, src)
		}
		t.Files = append(t.Files, f)
	}
	return t, nil
}

// ReadFile reads one file of the commit, such as a go.mod or a coverage report, with the same guards
// as the walk and at most max bytes. A file that is not read comes back as a skip, with the reason
// SkipMissing when it is not in the work tree.
func ReadFile(root, rel string, max int64) ([]byte, *report.Skip) {
	if !validPath(rel) {
		return nil, &report.Skip{Reason: report.SkipInvalidPath}
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, &report.Skip{Path: rel, Reason: report.SkipUnreadable, Detail: cause(err)}
	}
	defer r.Close()
	src, _, reason, err := readEntry(r, rel, max)
	if reason != "" {
		s := &report.Skip{Path: rel, Reason: reason}
		if err != nil {
			s.Detail = cause(err)
		}
		return nil, s
	}
	return src, nil
}

// ReadPath reads a file the user named on the command line, which may be outside the repository and
// may be a symlink the user chose. It is still opened without blocking, must be a regular file, and is
// read up to max bytes.
func ReadPath(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	src, _, reason, err := readCapped(f, max)
	switch {
	case err != nil:
		return nil, err
	case reason == report.SkipTooLarge:
		return nil, fmt.Errorf("%s is larger than %d MiB", path, max>>20)
	}
	return src, nil
}

// readEntry reads a path of the commit from the work tree, which may hold something else at the path
// than the commit does: nothing, a symlink, or a FIFO.
func readEntry(r *os.Root, rel string, max int64) (src []byte, size int64, reason report.SkipReason, err error) {
	fi, err := r.Lstat(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return nil, 0, report.SkipMissing, nil
	case err != nil:
		return nil, 0, report.SkipUnreadable, err
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, 0, report.SkipSymlink, nil
	case !fi.Mode().IsRegular():
		return nil, 0, report.SkipNotRegular, nil
	}
	return readSource(r, rel, max)
}

// readSource reads a regular file without leaving the root through a symlink swapped in after the walk
// saw it, without blocking on a FIFO swapped in (openFlags), and without reading past max even if the
// file grows. The type and size come from the open descriptor, not from the earlier lstat.
func readSource(r *os.Root, rel string, max int64) (src []byte, size int64, reason report.SkipReason, err error) {
	f, err := r.OpenFile(rel, os.O_RDONLY|openFlags, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, 0, report.SkipSymlink, nil
		}
		return nil, 0, report.SkipUnreadable, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, report.SkipUnreadable, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, report.SkipNotRegular, nil
	}
	if info.Size() > max {
		return nil, 0, report.SkipTooLarge, nil
	}
	return readCapped(f, max)
}

// readCapped reads at most max bytes; a file that grew past it after fstat counts as too large.
func readCapped(f io.Reader, max int64) (src []byte, size int64, reason report.SkipReason, err error) {
	src, err = io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, 0, report.SkipUnreadable, err
	}
	if int64(len(src)) > max {
		return nil, 0, report.SkipTooLarge, nil
	}
	return src, int64(len(src)), "", nil
}

// cause describes an error without the path it carries: reports must not contain the user's absolute
// paths.
func cause(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return "unreadable"
}

func countLines(src []byte) int {
	n := bytes.Count(src, []byte{'\n'})
	if len(src) > 0 && src[len(src)-1] != '\n' {
		n++
	}
	return n
}

func vendored(rel string) bool {
	for part := range strings.SplitSeq(rel, "/") {
		if vendorDirs[part] {
			return true
		}
	}
	return false
}
