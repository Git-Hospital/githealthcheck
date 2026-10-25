// Package gitx reads the history of the scanned repository through execx.Git and applies its
// .mailmap. git's own mailmap support is switched off (execx), because a .mailmap can name a FIFO or a
// blob that never ends; the file is read here through the walk's guards instead.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// logFormat starts each commit with \x01 and the hash, then the author name, email and time, separated
// by NUL. With -z, the changed paths follow after a newline as NUL-separated status and path fields,
// and a NUL ends the commit.
const logFormat = "--format=format:%x01%H%x00%an%x00%ae%x00%at"

// LogArgs are the git arguments for the history of head: non-merge commits with their changed paths.
// Only exact renames are detected: scoring a rename by similarity makes git inflate both blobs in
// full, whatever core.bigFileThreshold says, so a repository of a few hundred kilobytes could make it
// allocate gigabytes. Exact renames compare object names and read no blob.
func LogArgs(head string) []string {
	return []string{"log", "--no-merges", "--find-renames=100%", "--name-status", "-z", logFormat, head}
}

// ErrTooLarge is returned when the history lists more than MaxChanges changed paths. Each costs about
// 80 bytes once parsed and indexed, so the 512 MiB output cap alone does not bound the memory: a
// pack of half a megabyte can list 20 million.
var ErrTooLarge = errors.New("git log: too many changes")

// MaxChanges bounds the memory at a few hundred megabytes. Flask's history has about 9 thousand; one
// of hundreds of thousands of commits can be over it and gets too_large.
var MaxChanges = 5_000_000

// Log reads the history reachable from head.
func Log(ctx context.Context, git *execx.Git, head string) ([]facts.Commit, error) {
	args := LogArgs(head)
	out, err := git.Run(ctx, args[0], args[1:]...)
	if err != nil {
		return nil, err
	}
	return ParseLog(out)
}

// ParseLog parses the output of LogArgs. Fields are taken by position, so an author name holding \x01
// or a newline cannot shift the records. The fields are slices of out, read in place.
func ParseLog(out []byte) ([]facts.Commit, error) {
	r := &logReader{out: out, paths: map[string]string{}}
	var commits []facts.Commit
	for r.pos <= len(out) {
		head, _ := r.next()
		if len(head) == 0 && r.pos > len(out) {
			break // trailing NUL
		}
		c, err := r.commit(head)
		if err != nil {
			return nil, err
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// logReader reads the NUL-separated fields of git log's output.
type logReader struct {
	out     []byte
	pos     int // where the next field starts; past len(out) once the last one was taken
	changes int
	paths   map[string]string // a path is changed by many commits; one copy of it serves all of them
}

func (r *logReader) next() ([]byte, bool) {
	if r.pos > len(r.out) {
		return nil, false
	}
	end := bytes.IndexByte(r.out[r.pos:], 0)
	if end < 0 {
		t := r.out[r.pos:]
		r.pos = len(r.out) + 1
		return t, true
	}
	t := r.out[r.pos : r.pos+end]
	r.pos += end + 1
	return t, true
}

func (r *logReader) intern(b []byte) string {
	if s, ok := r.paths[string(b)]; ok {
		return s
	}
	s := string(b)
	r.paths[s] = s
	return s
}

// commit reads one commit: its head field, author, time and changes.
func (r *logReader) commit(head []byte) (facts.Commit, error) {
	hash, ok := bytes.CutPrefix(head, []byte{1})
	if !ok || !isHash(hash) {
		return facts.Commit{}, fmt.Errorf("git log: expected a commit at byte %d, got %q", r.pos, truncate(head))
	}
	name, ok1 := r.next()
	email, ok2 := r.next()
	when, ok3 := r.next()
	if !ok1 || !ok2 || !ok3 {
		return facts.Commit{}, fmt.Errorf("git log: commit %s is cut short", hash)
	}
	ts, status, hasChanges := bytes.Cut(when, []byte{'\n'})
	c := facts.Commit{Hash: string(hash), Name: string(name), Email: string(email)}
	// git prints a crafted or corrupt author time as it is stored, beyond int64, or as nothing.
	// The commit still counts; only its time is unknown.
	t, err := strconv.ParseInt(string(ts), 10, 64)
	c.Time, c.NoTime = t, err != nil
	if err != nil {
		c.Time = 0
	}
	// The NUL that ends the commit is an empty status.
	for hasChanges && len(status) > 0 {
		ch, err := r.change(hash, status)
		if err != nil {
			return facts.Commit{}, err
		}
		if r.changes++; r.changes > MaxChanges {
			return facts.Commit{}, ErrTooLarge
		}
		c.Changes = append(c.Changes, ch)
		status, hasChanges = r.next()
	}
	return c, nil
}

// change reads the paths of one change with the given status.
func (r *logReader) change(hash, status []byte) (facts.Change, error) {
	ch := facts.Change{Status: status[0]}
	switch ch.Status {
	case 'R', 'C':
		old, ok1 := r.next()
		path, ok2 := r.next()
		if !ok1 || !ok2 {
			return ch, fmt.Errorf("git log: commit %s: rename cut short", hash)
		}
		ch.Old, ch.Path = r.intern(old), r.intern(path)
	case 'A', 'M', 'D', 'T', 'U', 'X', 'B':
		path, ok := r.next()
		if !ok {
			return ch, fmt.Errorf("git log: commit %s: change cut short", hash)
		}
		ch.Path = r.intern(path)
	default:
		return ch, fmt.Errorf("git log: commit %s: unknown status %q", hash, truncate(status))
	}
	return ch, nil
}

func isHash(b []byte) bool {
	if len(b) != 40 && len(b) != 64 {
		return false
	}
	for _, c := range b {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return strings.ToValidUTF8(s, "?")
}
