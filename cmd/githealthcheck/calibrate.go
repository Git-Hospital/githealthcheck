package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/report"
	"github.com/Git-Hospital/githealthcheck/internal/score"
)

// corpusEntry is one line of calibration/corpus.txt.
type corpusEntry struct {
	language, name, commit string
}

var (
	// GitHub's own rules: an owner of letters, digits and inner hyphens; a repository name of letters,
	// digits, '.', '_' and '-', not starting with '-'.
	repoName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// readCorpus parses corpus.txt: a language, a GitHub owner/name and a full commit SHA per line, with
// # comments and blank lines.
func readCorpus(data []byte) ([]corpusEntry, error) {
	var out []corpusEntry
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
			continue
		case len(f) != 3:
			return nil, fmt.Errorf("line %d: want a language, a repository and a commit", n)
		case f[0] != "go" && f[0] != "python":
			return nil, fmt.Errorf("line %d: language %q is not analysed", n, f[0])
		case !repoName.MatchString(f[1]) || strings.Contains(f[1], ".."):
			return nil, fmt.Errorf("line %d: %q is not a GitHub owner/name", n, f[1])
		case !commitSHA.MatchString(f[2]):
			return nil, fmt.Errorf("line %d: %q is not a full commit SHA", n, f[2])
		case seen[f[1]]:
			return nil, fmt.Errorf("line %d: %s is listed twice", n, f[1])
		}
		seen[f[1]] = true
		out = append(out, corpusEntry{f[0], f[1], f[2]})
	}
	return out, sc.Err()
}

// calibrate measures every repository of the corpus at its commit and writes the lock, or with
// --check compares what it measured with the lock and exits 1 when they differ.
func calibrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	corpusFile := fs.String("corpus", "calibration/corpus.txt", "the corpus list")
	lockFile := fs.String("lock", "calibration/corpus.lock", "the lock to write, or with --check to compare with")
	work := fs.String("work", "", "clone into this directory (default: a temporary one)")
	keep := fs.Bool("keep", false, "keep the clones in --work after measuring them")
	check := fs.Bool("check", false, "compare with the lock instead of writing it; exit 1 when they differ")
	timeout := fs.Duration("timeout", 15*time.Minute, "give up on one repository after this long")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return report.ExitError
	}
	data, err := os.ReadFile(*corpusFile)
	if err != nil {
		fmt.Fprintln(stderr, "githealthcheck:", err)
		return report.ExitError
	}
	corpus, err := readCorpus(data)
	if err != nil {
		fmt.Fprintf(stderr, "githealthcheck: %s: %v\n", *corpusFile, err)
		return report.ExitError
	}
	if *keep && *work == "" {
		fmt.Fprintln(stderr, "githealthcheck: --keep needs --work")
		return report.ExitError
	}
	if *work == "" {
		if *work, err = os.MkdirTemp("", "githealthcheck-calibrate-"); err != nil {
			fmt.Fprintln(stderr, "githealthcheck:", err)
			return report.ExitError
		}
		defer os.RemoveAll(*work)
	}

	lock := &score.Lock{Format: score.Format, Tool: version}
	tools := map[string]report.Tool{}
	for i, e := range corpus {
		start := time.Now()
		r, err := measure(e, *work, *keep, *timeout)
		if err != nil {
			fmt.Fprintf(stderr, "githealthcheck: %s: %v\n", e.name, err)
			return report.ExitError
		}
		rp := score.Repo{Language: e.language, Name: e.name, Commit: e.commit, Metrics: map[string]report.Measurement{}}
		for _, f := range r.Families {
			for m, v := range f.Metrics {
				rp.Metrics[m] = v
			}
		}
		for _, t := range r.Inputs.Tools {
			tools[t.Name] = t
		}
		lock.Repos = append(lock.Repos, rp)
		fmt.Fprintf(stderr, "[%d/%d] %s %s (%s)\n", i+1, len(corpus), e.name, e.commit[:12], time.Since(start).Round(time.Second))
	}
	for _, name := range []string{"python3"} {
		if t, ok := tools[name]; ok {
			lock.Tools = append(lock.Tools, t)
		}
	}
	lock.Distributions()
	body, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "githealthcheck:", err)
		return report.ExitError
	}
	body = append(body, '\n')

	if !*check {
		if err := writeFile(*lockFile, body); err != nil {
			fmt.Fprintln(stderr, "githealthcheck:", err)
			return report.ExitError
		}
		fmt.Fprintf(stdout, "wrote %s: %d repositories\n", *lockFile, len(lock.Repos))
		return report.ExitClean
	}
	old, err := os.ReadFile(*lockFile)
	if err == nil {
		var was *score.Lock
		if was, err = score.Load(old); err == nil {
			diffs := lockDiff(was, lock)
			for _, d := range diffs {
				fmt.Fprintln(stdout, d)
			}
			if len(diffs) > 0 {
				return report.ExitFindings
			}
			fmt.Fprintf(stdout, "%s matches: %d repositories\n", *lockFile, len(lock.Repos))
			return report.ExitClean
		}
	}
	fmt.Fprintln(stderr, "githealthcheck:", err)
	return report.ExitError
}

// measure clones one corpus repository without blobs, checks out its commit, which fetches the blobs
// of that commit only, and scans it. Each run clones afresh, so no config left in a clone is trusted;
// the clone is removed afterwards unless keep is set. A scan that did not finish, or whose work tree
// differs from the commit (a checkout that cannot be exact on this file system), is refused: the lock
// holds only complete measurements of the commit.
func measure(e corpusEntry, work string, keep bool, timeout time.Duration) (*report.Report, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	dir := filepath.Join(work, strings.ReplaceAll(e.name, "/", "__"))
	if !keep {
		defer os.RemoveAll(dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := fetchGit(ctx, "", "clone", "--quiet", "--filter=blob:none", "--no-checkout", "--", "https://github.com/"+e.name+".git", dir); err != nil {
		return nil, err
	}
	if err := fetchGit(ctx, dir, "-c", "advice.detachedHead=false", "checkout", "--quiet", "--force", "--detach", e.commit); err != nil {
		return nil, err
	}
	r, _, err := buildReport(ctx, dir, "")
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if report.ExitCode(r, nil) == report.ExitIncomplete {
		return nil, fmt.Errorf("the scan is incomplete: a family or metric timed out or its tool crashed")
	}
	if r.Repository.Dirty {
		return nil, fmt.Errorf("the checkout differs from the commit in %d files", r.Repository.ModifiedFiles)
	}
	if lang, share := score.Language(r.Languages); lang != e.language || share < score.MinAnalysedShare {
		return nil, fmt.Errorf("scanned as %s with %.0f%% of its code analysed, listed as %s", lang, 100*share, e.language)
	}
	return r, nil
}

// fetchGit runs the git commands that fetch the corpus, the only ones githealthcheck runs with network
// access: HTTPS only (GIT_ALLOW_PROTOCOL, which a repository's config cannot widen), without the
// user's or the system's git config (so no credential helper, no filters such as Git LFS), without
// hooks, fsmonitor or a terminal prompt.
func fetchGit(ctx context.Context, dir string, args ...string) error {
	full := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}
	if dir != "" {
		full = append(full, "-C", dir)
	}
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Env = append(execx.Env(os.Environ()), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=https")
	// A transfer runs in child processes (git-remote-https) that hold stderr: past the deadline, Wait
	// stops waiting for them.
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// lockDiff lists what differs between two locks: repositories, commits and metric values. The
// githealthcheck version is not compared, the external tools are.
func lockDiff(was, now *score.Lock) []string {
	var out []string
	if !slices.Equal(was.Tools, now.Tools) {
		out = append(out, fmt.Sprintf("tools: %v, now %v", was.Tools, now.Tools))
	}
	old := map[string]score.Repo{}
	for _, r := range was.Repos {
		old[r.Name] = r
	}
	for _, r := range now.Repos {
		o, ok := old[r.Name]
		delete(old, r.Name)
		switch {
		case !ok:
			out = append(out, r.Name+": not in the lock")
			continue
		case o.Commit != r.Commit || o.Language != r.Language:
			out = append(out, fmt.Sprintf("%s: %s %s in the lock, %s %s now", r.Name, o.Language, o.Commit, r.Language, r.Commit))
			continue
		}
		names := map[string]bool{}
		for m := range o.Metrics {
			names[m] = true
		}
		for m := range r.Metrics {
			names[m] = true
		}
		for _, m := range slices.Sorted(maps.Keys(names)) {
			if a, b := valueText(o.Metrics[m]), valueText(r.Metrics[m]); a != b {
				out = append(out, fmt.Sprintf("%s %s: %s in the lock, %s now", r.Name, m, a, b))
			}
		}
	}
	for name := range old {
		out = append(out, name+": in the lock, not in the corpus")
	}
	slices.Sort(out)
	return out
}

func valueText(m report.Measurement) string {
	if m.IsNull() {
		return "null (" + string(m.Reason) + ")"
	}
	return fmt.Sprint(*m.Value)
}
