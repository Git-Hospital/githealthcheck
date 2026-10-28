package repo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// WalkObjects reads the commit's files in entries from the repository's object store instead of the
// work tree (ADR 0002, E5), so a past commit can be measured without a checkout: nothing is written to
// disk, and what the work tree holds does not matter. It reads the same entries as Walk, with the same
// size bound, and gives the same Tree for a commit that is checked out unchanged.
//
// git cat-file runs twice. --batch-check gives each object's type and size; then --batch streams only
// the blobs within MaxSourceBytes, one at a time, so memory holds one file. The object names come from
// the entries only, and each must be a full name in the repository's object format, so nothing a
// repository wrote reaches cat-file's stdin but hexadecimal digits.
func WalkObjects(ctx context.Context, git *execx.Git, entries []Entry, blobID func([]byte) string) (*Tree, error) {
	size, ok := oidSize[git.ObjectFormat()]
	if !ok {
		return nil, fmt.Errorf("unknown object format %q", git.ObjectFormat())
	}
	return walk(ctx, git.Root(), objects{git: git, oidLen: size}, entries, blobID)
}

// oidSize is the length of a full object name in each object format.
var oidSize = map[string]int{"sha1": 40, "sha256": 64}

// errObjectMissing is a blob the object store does not have, as in a partial clone. It is never
// fetched.
var errObjectMissing = fmt.Errorf("object missing: %w", fs.ErrNotExist)

// errProtocol is cat-file output that does not answer what was asked.
var errProtocol = errors.New("unexpected cat-file output")

// errInvalidName is a tree entry whose object name is not a full name in the object format. ls-tree
// never prints one; it is checked so that nothing but hexadecimal digits reaches cat-file.
var errInvalidName = errors.New("invalid object name")

// objects reads blobs with git cat-file.
type objects struct {
	git    *execx.Git
	oidLen int
}

// object is what --batch-check said about one object.
type object struct {
	kind string // blob, tree, commit, tag; "" when missing
	size int64
}

func (o objects) each(ctx context.Context, entries []Entry, fn func(Entry, content)) error {
	var asked []string
	for _, e := range entries {
		if wanted(e) && o.valid(e.OID) {
			asked = append(asked, e.OID)
		}
	}
	info, err := o.check(ctx, asked)
	if err != nil {
		return err
	}
	// The blobs to stream, in the entries' order, and the exact length of their --batch output.
	var fetch []string
	var limit int64
	for i, oid := range asked {
		if info[i].kind == "blob" && info[i].size <= MaxSourceBytes {
			fetch = append(fetch, oid)
			limit += int64(len(header(oid, info[i].size))) + info[i].size + 1
		}
	}
	visit := func(next func(oid string, size int64) ([]byte, error)) error {
		i := 0 // the next object of asked
		for _, e := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var c content
			if wanted(e) {
				var err error
				if c, err = o.content(e, info, &i, next); err != nil {
					// The stream is out of step from here on, so the walk stops.
					return err
				}
			}
			fn(e, c)
		}
		return nil
	}
	if len(fetch) == 0 {
		return visit(nil)
	}
	err = o.git.Pipe(ctx, names(fetch), limit, func(r io.Reader) error {
		br := bufio.NewReader(r)
		return visit(func(oid string, size int64) ([]byte, error) { return readObject(br, oid, size) })
	}, "cat-file", "--batch", "--buffer")
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// content is the content of the wanted entry e, whose object, if its name is valid, is asked[*i].
func (o objects) content(e Entry, info []object, i *int, next func(string, int64) ([]byte, error)) (content, error) {
	if !o.valid(e.OID) {
		return content{reason: report.SkipUnreadable, err: errInvalidName}, nil
	}
	obj := info[*i]
	*i++
	switch {
	case obj.kind == "":
		return content{reason: report.SkipUnreadable, err: errObjectMissing}, nil
	case obj.kind != "blob":
		return content{reason: report.SkipNotRegular}, nil
	case obj.size > MaxSourceBytes:
		return content{reason: report.SkipTooLarge}, nil
	}
	src, err := next(e.OID, obj.size)
	if err != nil {
		return content{}, err
	}
	return content{src: src, size: obj.size}, nil
}

// valid reports whether oid is a full object name in lower-case hexadecimal, as ls-tree prints it.
func (o objects) valid(oid string) bool {
	if len(oid) != o.oidLen {
		return false
	}
	for _, c := range []byte(oid) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// check asks --batch-check for the type and size of each object, in order.
func (o objects) check(ctx context.Context, asked []string) ([]object, error) {
	if len(asked) == 0 {
		return nil, nil
	}
	info := make([]object, 0, len(asked))
	// "<name> <type> <size>\n", where the type is at most 6 bytes and the size at most 19 digits.
	limit := int64(len(asked)) * int64(o.oidLen+28)
	err := o.git.Pipe(ctx, names(asked), limit, func(r io.Reader) error {
		br := bufio.NewReader(r)
		for _, oid := range asked {
			obj, err := readCheck(br, oid)
			if err != nil {
				return err
			}
			info = append(info, obj)
		}
		return nil
	}, "cat-file", "--batch-check", "--buffer")
	if err != nil {
		return nil, err
	}
	return info, nil
}

// names is cat-file's stdin: one object name per line.
func names(oids []string) io.Reader {
	return strings.NewReader(strings.Join(oids, "\n") + "\n")
}

// header is the line --batch prints before an object's content.
func header(oid string, size int64) string {
	return oid + " blob " + strconv.FormatInt(size, 10) + "\n"
}

// readCheck parses one --batch-check line for oid: "<oid> <type> <size>" or "<oid> missing".
func readCheck(br *bufio.Reader, oid string) (object, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return object{}, fmt.Errorf("%w: %v", errProtocol, err)
	}
	fields := strings.Fields(line)
	switch {
	case len(fields) == 2 && fields[0] == oid && fields[1] == "missing":
		return object{}, nil
	case len(fields) != 3 || fields[0] != oid:
		return object{}, errProtocol
	}
	switch fields[1] {
	case "blob", "tree", "commit", "tag":
	default:
		return object{}, errProtocol
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return object{}, errProtocol
	}
	return object{kind: fields[1], size: size}, nil
}

// readObject reads one --batch answer: the header for oid with the size --batch-check gave, the
// content, and a newline.
func readObject(br *bufio.Reader, oid string, size int64) ([]byte, error) {
	line, err := br.ReadString('\n')
	if err != nil || line != header(oid, size) {
		return nil, errProtocol
	}
	src := make([]byte, size+1)
	if _, err := io.ReadFull(br, src); err != nil || src[size] != '\n' {
		return nil, errProtocol
	}
	return src[:size], nil
}
