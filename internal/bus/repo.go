package bus

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/gitx"
)

// ErrRejected means the remote refused a push because the ref wasn't what the push expected:
// someone else moved it first. Any other failure (network, auth, a server-side rule) is a
// different error.
var ErrRejected = errors.New("push rejected: the ref moved")

// Commit is a bus commit as read back.
type Commit struct {
	SHA     string
	Time    time.Time // committer time, the writer's clock
	Message string
}

// PushMode chooses how a push may move the remote ref.
type PushMode int

const (
	// FastForward accepts only a descendant of the remote's current tip.
	FastForward PushMode = iota
	// Force replaces whatever is there (presence, which has no history).
	Force
	// Lease accepts only if the remote ref is exactly the expected value: a compare-and-swap.
	Lease
)

// Repo is the bus as seen from one clone.
type Repo struct {
	Git    gitx.Git
	Remote string
	// Name and Email sign bus commits as the device, so no commit-tree depends on the user's
	// git identity being configured.
	Name, Email string

	emptyTree string
}

// EmptyTree returns the ID of git's empty tree, which every bus commit carries: bus commits
// hold no files, so fetching them brings nothing else along.
func (r *Repo) EmptyTree(ctx context.Context) (string, error) {
	if r.emptyTree != "" {
		return r.emptyTree, nil
	}
	out, err := r.Git.Run(ctx, "", "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	r.emptyTree = strings.TrimSpace(out)
	return r.emptyTree, nil
}

// NewCommit writes a commit with the empty tree and the given parent ("" for a root commit).
// It writes an object and nothing else: no ref moves until a push succeeds.
func (r *Repo) NewCommit(ctx context.Context, parent, message string) (string, error) {
	tree, err := r.EmptyTree(ctx)
	if err != nil {
		return "", err
	}
	// Unsigned on purpose: a signing prompt on every message would make the bus unusable, and
	// these commits never reach a branch.
	args := []string{"-c", "user.name=" + r.Name, "-c", "user.email=" + r.Email,
		"commit-tree", "--no-gpg-sign", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-F", "-")
	out, err := r.Git.Run(ctx, message, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ListRemote returns every bus ref on the remote and its SHA. It is one round trip and
// transfers no objects, which is why the daemon can afford it every few seconds.
func (r *Repo) ListRemote(ctx context.Context) (map[string]string, error) {
	out, err := r.Git.Run(ctx, "", "ls-remote", r.Remote, brand.RefRoot+"*")
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && strings.HasPrefix(ref, brand.RefRoot) {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// RemoteRef returns one ref's SHA on the remote, or "" if it doesn't exist there.
func (r *Repo) RemoteRef(ctx context.Context, ref string) (string, error) {
	out, err := r.Git.Run(ctx, "", "ls-remote", r.Remote, ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok && name == ref {
			return sha, nil
		}
	}
	return "", nil
}

// Fetch copies the given remote refs to the same names locally. The local copies are a cache
// for reading history; what the daemon has processed is tracked separately, so a fetch that
// lands late and rewinds a local copy does no harm.
func (r *Repo) Fetch(ctx context.Context, refs []string) error {
	if len(refs) == 0 {
		return nil
	}
	args := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--quiet",
		"--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", r.Remote}
	for _, ref := range refs {
		args = append(args, "+"+ref+":"+ref)
	}
	_, err := r.Git.Run(ctx, "", args...)
	return err
}

// Push sets a remote ref to sha, or deletes it if sha is "". With Lease, expect is the value
// the remote ref must have ("" meaning it must not exist), checked atomically by the server.
//
// Pushes skip the repo's hooks: a pre-push hook that runs the test suite on every message would
// make the bus unusable, and these commits never reach a branch.
func (r *Repo) Push(ctx context.Context, sha, ref string, mode PushMode, expect string) error {
	args := []string{"push", "--porcelain", "--no-verify", "--no-follow-tags", "--no-signed",
		"--recurse-submodules=no", r.Remote}
	switch mode {
	case Force:
		args = append(args, "--force")
	case Lease:
		args = append(args, "--force-with-lease="+ref+":"+expect)
	}
	args = append(args, sha+":"+ref)
	out, err := r.Git.Run(ctx, "", args...)
	var refused string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "!\t") {
			refused = strings.TrimSpace(line)
		}
	}
	if err == nil && refused == "" {
		return nil
	}
	// A failed push may still have landed: the server can update the ref and then fail to
	// report it ("remote failed to report status"), or the connection can drop after. Calling
	// that a failure would make the caller write the same event twice, so look.
	// The push's own deadline may be what failed it, so look with a fresh one.
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	now, lerr := r.RemoteRef(lctx, ref)
	if lerr == nil && now == sha {
		return nil
	}
	if refused == "" {
		return err // network, auth: nothing was decided
	}
	if !strings.Contains(refused, "remote rejected") {
		return ErrRejected // refused before sending: the ref had already moved
	}
	// The server refused. When two leased pushes race, the loser can pass the client-side
	// check and still lose on the server, which reports it as a rejection ("reference already
	// exists", "cannot lock ref"). If the ref moved, that is what happened.
	if mode == Lease && lerr == nil && now != expect {
		return ErrRejected
	}
	return fmt.Errorf("push to %s refused by the server: %s", ref, refused)
}

// LocalRefs returns the local copies of bus refs.
func (r *Repo) LocalRefs(ctx context.Context) (map[string]string, error) {
	out, err := r.Git.Run(ctx, "", "for-each-ref", "--format=%(objectname) %(refname)", brand.RefRoot)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ref, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// UpdateLocal points a local bus ref at sha.
func (r *Repo) UpdateLocal(ctx context.Context, ref, sha string) error {
	_, err := r.Git.Run(ctx, "", "update-ref", ref, sha)
	return err
}

// IsAncestor reports whether a is an ancestor of b (or equal to it). A commit that isn't
// present locally counts as not an ancestor. Any other failure is an error, so that a passing
// fault isn't taken for rewritten history, which callers handle by starting over.
func (r *Repo) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	_, err := r.Git.Run(ctx, "", "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	for _, c := range []string{a, b} {
		// cat-file -e exits 1, and only 1, for an object that isn't here.
		if _, cerr := r.Git.Run(ctx, "", "cat-file", "-e", c); errors.As(cerr, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
	}
	return false, err
}

// recordSep and fieldSep cannot appear in what t4 writes: subjects are collapsed to one line
// and the JSON body escapes control characters.
const (
	recordSep = "\x1e"
	fieldSep  = "\x00"
)

// Log returns the commits after from (exclusive; "" for the start) up to to, oldest first.
func (r *Repo) Log(ctx context.Context, from, to string) ([]Commit, error) {
	args := []string{"log", "--no-show-signature", "--no-color", "--reverse",
		"--format=%H%x00%ct%x00%B%x1e", to}
	if from != "" {
		args = append(args, "^"+from)
	}
	out, err := r.Git.Run(ctx, "", append(args, "--")...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, recordSep) {
		rec = strings.TrimLeft(rec, "\r\n")
		parts := strings.SplitN(rec, fieldSep, 3)
		if len(parts) != 3 {
			continue
		}
		secs, _ := strconv.ParseInt(parts[1], 10, 64)
		commits = append(commits, Commit{SHA: parts[0], Time: time.Unix(secs, 0), Message: parts[2]})
	}
	return commits, nil
}

// ReadCommit returns one commit.
func (r *Repo) ReadCommit(ctx context.Context, sha string) (Commit, error) {
	out, err := r.Git.Run(ctx, "", "log", "-1", "--no-show-signature", "--no-color",
		"--format=%H%x00%ct%x00%B", sha, "--")
	if err != nil {
		return Commit{}, err
	}
	parts := strings.SplitN(out, fieldSep, 3)
	if len(parts) != 3 {
		return Commit{}, fmt.Errorf("unreadable commit %s", sha)
	}
	secs, _ := strconv.ParseInt(parts[1], 10, 64)
	return Commit{SHA: parts[0], Time: time.Unix(secs, 0), Message: parts[2]}, nil
}

// Config returns a git config value, or "" if it isn't set.
func (r *Repo) Config(ctx context.Context, key string) string {
	out, err := r.Git.Run(ctx, "", "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
