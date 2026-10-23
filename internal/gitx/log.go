// Package gitx reads the history of the scanned repository through execx.Git and applies its
// .mailmap. git's own mailmap support is switched off (execx), because a .mailmap can name a FIFO or a
// blob that never ends; the file is read here through the walk's guards instead.
package gitx

import (
	"bytes"
	"context"
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

// LogArgs are the git arguments for the history of head: non-merge commits with their changed paths,
// renames detected as in Avelino et al.'s truck-factor tool.
func LogArgs(head string) []string {
	return []string{"log", "--no-merges", "-M", "--name-status", "-z", logFormat, head}
}

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
// or a newline cannot shift the records.
func ParseLog(out []byte) ([]facts.Commit, error) {
	tokens := bytes.Split(out, []byte{0})
	var commits []facts.Commit
	i := 0
	next := func() ([]byte, bool) {
		if i >= len(tokens) {
			return nil, false
		}
		t := tokens[i]
		i++
		return t, true
	}
	for i < len(tokens) {
		head, _ := next()
		if len(head) == 0 && i == len(tokens) {
			break // trailing NUL
		}
		hash, ok := bytes.CutPrefix(head, []byte{1})
		if !ok || !isHash(hash) {
			return nil, fmt.Errorf("git log: expected a commit at field %d, got %q", i, truncate(head))
		}
		name, ok1 := next()
		email, ok2 := next()
		when, ok3 := next()
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("git log: commit %s is cut short", hash)
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
		for hasChanges {
			if len(status) == 0 {
				break // the NUL that ends the commit
			}
			ch := facts.Change{Status: status[0]}
			switch ch.Status {
			case 'R', 'C':
				old, ok1 := next()
				path, ok2 := next()
				if !ok1 || !ok2 {
					return nil, fmt.Errorf("git log: commit %s: rename cut short", hash)
				}
				ch.Old, ch.Path = string(old), string(path)
			case 'A', 'M', 'D', 'T', 'U', 'X', 'B':
				path, ok := next()
				if !ok {
					return nil, fmt.Errorf("git log: commit %s: change cut short", hash)
				}
				ch.Path = string(path)
			default:
				return nil, fmt.Errorf("git log: commit %s: unknown status %q", hash, truncate(status))
			}
			c.Changes = append(c.Changes, ch)
			if status, hasChanges = next(); !hasChanges {
				break
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
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
