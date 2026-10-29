// Package attrib compares worktree HEAD snapshots to find commits made during a tool call.
// Its git commands only read refs and objects; they never change the worktree or index.
package attrib

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/a-Fig/t4/internal/gitx"
)

// Ref is a worktree's HEAD. SHA is empty before the first commit; Branch is empty when detached.
// Values returned by Head also retain existing ref tips for branch-switch attribution. Keep
// that snapshot with the value: constructing a Ref from just SHA and Branch loses that baseline.
type Ref struct {
	SHA    string
	Branch string
	tips   []string
}

// Commit is a newly reachable commit. Branch is the after snapshot's branch, since git commits
// themselves have no branch membership; it is empty for a detached HEAD.
type Commit struct {
	SHA     string
	Subject string
	Branch  string
}

// Head reads this worktree's own HEAD, including in linked worktrees. It snapshots every ref
// with for-each-ref, so switching to existing history (including remote branches and tags)
// does not attribute that history to the call. This costs a scan of refs and a commit peel for
// each annotated tag. A commit another process makes to a ref that did not exist at the snapshot
// can still be attributed to this call: two snapshots cannot establish who created an object.
func Head(ctx context.Context, worktree string) (Ref, error) {
	var ref Ref
	// RunIn uses exec.CommandContext and the repository's git error/cancellation conventions.
	branch, err := gitx.RunIn(ctx, worktree, "symbolic-ref", "--quiet", "HEAD")
	if err != nil && !exitCode(err, 1) {
		return Ref{}, err
	}
	branch = strings.TrimSpace(branch)
	ref.Branch = strings.TrimPrefix(branch, "refs/heads/")
	out, err := gitx.RunIn(ctx, worktree, "for-each-ref", "--format=%(refname)%00%(objecttype)%00%(objectname)")
	if err != nil {
		return Ref{}, err
	}
	headExists := false
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\x00")
		if len(fields) != 3 {
			return Ref{}, fmt.Errorf("unexpected git for-each-ref output: %q", line)
		}
		headExists = headExists || fields[0] == branch
		switch fields[1] {
		case "commit":
			ref.tips = append(ref.tips, fields[2])
		case "tag":
			// Peel recursively, including tags of tags; tags of trees/blobs have no history.
			sha, err := gitx.RunIn(ctx, worktree, "rev-parse", "--verify", "--quiet", "--end-of-options", fields[2]+"^{commit}")
			if err != nil {
				if exitCode(err, 1) {
					continue
				}
				return Ref{}, err
			}
			ref.tips = append(ref.tips, strings.TrimSpace(sha))
		}
	}
	sha, err := gitx.RunIn(ctx, worktree, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		if exitCode(err, 1) && branch != "" && !headExists {
			return ref, nil // A symbolic HEAD whose branch does not exist yet is unborn.
		}
		return Ref{}, err
	}
	ref.SHA = strings.TrimSpace(sha)
	return ref, nil
}

// NewCommits returns after's history minus before's history, in oldest-first topological order.
// On a branch switch it also excludes history reachable from any ref snapshotted by Head before
// the call. Without that baseline, a checkout alone could look like a batch of new commits.
//
// An amend returns its replacement commit; a backwards reset returns nothing. A rebase on the
// same branch returns rewritten commits and any newly reachable upstream commits: their SHAs
// are reachable from after but not before, even when rewritten patches are unchanged.
func NewCommits(ctx context.Context, worktree string, before, after Ref) ([]Commit, error) {
	if after.SHA == "" {
		return nil, nil
	}
	args := []string{"log", "--reverse", "--topo-order", "--no-show-signature", "--encoding=UTF-8", "--format=%H%x00%s", "-z", "--end-of-options", after.SHA}
	if before.SHA != "" {
		args = append(args, "^"+before.SHA)
	}
	if before.Branch != after.Branch {
		for _, tip := range before.tips {
			args = append(args, "^"+tip)
		}
	}
	args = append(args, "--")
	out, err := gitx.RunIn(ctx, worktree, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	if out == "" {
		return commits, nil
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("unexpected git log output: %q", out)
	}
	for i := 0; i < len(fields); i += 2 {
		commits = append(commits, Commit{SHA: fields[i], Subject: fields[i+1], Branch: after.Branch})
	}
	return commits, nil
}

func exitCode(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}
