package bus_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
)

func logRepo(t *testing.T) *bus.Repo {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return &bus.Repo{Git: gitx.Git{Dir: dir}, Name: "test", Email: "test@example.invalid"}
}

// chain commits msgs one on another, and returns their SHAs, oldest first.
func chain(t *testing.T, r *bus.Repo, parent string, msgs ...string) []string {
	t.Helper()
	var shas []string
	for _, m := range msgs {
		sha, err := r.NewCommit(context.Background(), parent, m)
		if err != nil {
			t.Fatal(err)
		}
		shas, parent = append(shas, sha), sha
	}
	return shas
}

func readLog(t *testing.T, r *bus.Repo, from, to string, max int64) (pages [][]bus.Commit) {
	t.Helper()
	err := r.Log(context.Background(), from, to, max, func(p []bus.Commit) error {
		pages = append(pages, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

// A long history comes a page at a time, oldest first, each commit once and whole, and a read from
// the middle has only what came after.
func TestLogReadsALongHistoryInPages(t *testing.T) {
	r := logRepo(t)
	r.Page = 7
	var msgs []string
	for i := range 50 {
		msgs = append(msgs, fmt.Sprintf("event %d\n\n{\"n\":%d}\n", i, i))
	}
	shas := chain(t, r, "", msgs...)

	pages := readLog(t, r, "", shas[len(shas)-1], bus.MaxRecord)
	if len(pages) != 8 {
		t.Errorf("%d pages, want 8 of at most 7", len(pages))
	}
	var got []bus.Commit
	for _, p := range pages {
		if len(p) == 0 || len(p) > 7 {
			t.Errorf("a page of %d", len(p))
		}
		got = append(got, p...)
	}
	if len(got) != len(shas) {
		t.Fatalf("read %d commits, want %d", len(got), len(shas))
	}
	for i, c := range got {
		if c.SHA != shas[i] || c.Oversize || strings.TrimSpace(c.Message) != strings.TrimSpace(msgs[i]) || c.Time.IsZero() {
			t.Fatalf("commit %d: %+v, want %s with %q", i, c, shas[i], msgs[i])
		}
	}

	got = nil
	for _, p := range readLog(t, r, shas[40], shas[len(shas)-1], bus.MaxRecord) {
		got = append(got, p...)
	}
	if len(got) != 9 || got[0].SHA != shas[41] {
		t.Errorf("read from the middle: %d commits starting %s, want 9 from %s", len(got), got[0].SHA, shas[41])
	}
}

// A commit over the limit isn't read: it comes in its place, marked, with no message, so the reader
// passes over it and its cursor moves past it, and what follows it is read as usual. ReadCommit
// refuses it.
func TestLogPassesOverACommitOverTheLimit(t *testing.T) {
	r := logRepo(t)
	const max = 4 << 10
	huge := "huge\n\n{\"body\":\"" + strings.Repeat("x", 4*max) + "\"}\n"
	shas := chain(t, r, "", "before\n\n{}\n", huge, "after\n\n{}\n")

	var got []bus.Commit
	for _, p := range readLog(t, r, "", shas[2], max) {
		got = append(got, p...)
	}
	if len(got) != 3 {
		t.Fatalf("read %d commits, want 3", len(got))
	}
	if c := got[1]; c.SHA != shas[1] || !c.Oversize || c.Message != "" || c.Size <= max {
		t.Errorf("the large commit came as %+v", c)
	}
	if got[0].Oversize || got[2].Oversize || !strings.HasPrefix(got[2].Message, "after") {
		t.Errorf("the commits around it: %+v, %+v", got[0], got[2])
	}

	if _, err := r.ReadCommit(context.Background(), shas[1], max); !errors.Is(err, bus.ErrTooLarge) {
		t.Errorf("ReadCommit of the large commit: %v, want ErrTooLarge", err)
	}
	if c, err := r.ReadCommit(context.Background(), shas[1], 8*max); err != nil || !strings.HasPrefix(c.Message, "huge") {
		t.Errorf("ReadCommit within a higher limit: %v", err)
	}
	if c, err := r.ReadCommit(context.Background(), shas[2], max); err != nil || c.SHA != shas[2] || !strings.HasPrefix(c.Message, "after") {
		t.Errorf("ReadCommit of a small commit: %+v, %v", c, err)
	}
}

// An error from the page stops the read, and is what Log returns.
func TestLogStopsAtAPageError(t *testing.T) {
	r := logRepo(t)
	r.Page = 2
	shas := chain(t, r, "", "a\n\n{}\n", "b\n\n{}\n", "c\n\n{}\n", "d\n\n{}\n")
	stop := errors.New("stop")
	calls := 0
	err := r.Log(context.Background(), "", shas[3], bus.MaxRecord, func([]bus.Commit) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Errorf("Log returned %v after %d pages, want the page's error after 1", err, calls)
	}
}

// A log with a merge in it is read along first parents, so that wherever a read stops, the last
// commit it read stands for everything before it: a read resumed from there repeats nothing. A
// merge's other side, which Murmur never writes, isn't read.
func TestLogResumedPastAMergeRepeatsNothing(t *testing.T) {
	r := logRepo(t)
	ctx := context.Background()
	root := chain(t, r, "", "root\n\n{}\n")[0]
	a := chain(t, r, root, "a\n\n{}\n")[0]
	b := chain(t, r, root, "b\n\n{}\n")[0]
	tree, err := r.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Git.Run(ctx, "merge\n\n{}\n", "-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"commit-tree", "--no-gpg-sign", tree, "-p", a, "-p", b, "-F", "-")
	if err != nil {
		t.Fatal(err)
	}
	merge := strings.TrimSpace(out)
	tip := chain(t, r, merge, "after\n\n{}\n")[0]

	var all []string
	for _, p := range readLog(t, r, root, tip, bus.MaxRecord) {
		for _, c := range p {
			all = append(all, c.SHA)
		}
	}
	if want := []string{a, merge, tip}; strings.Join(all, " ") != strings.Join(want, " ") {
		t.Fatalf("read %v, want %v (first parents only)", all, want)
	}
	for i, cursor := range all {
		var rest []string
		for _, p := range readLog(t, r, cursor, tip, bus.MaxRecord) {
			for _, c := range p {
				rest = append(rest, c.SHA)
			}
		}
		if strings.Join(rest, " ") != strings.Join(all[i+1:], " ") {
			t.Errorf("resumed after %s: read %v, want %v", cursor, rest, all[i+1:])
		}
	}
}
