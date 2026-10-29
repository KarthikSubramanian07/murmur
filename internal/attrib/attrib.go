// Package attrib finds the commits a command made in a worktree by comparing HEAD snapshots.
// Its git commands only read refs and objects; they never change the worktree or index.
package attrib

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/t4/internal/gitx"
)

// committerSlack is how far before Ref.Time a committer date may fall and still count as made
// during the command. Git stores committer dates in whole seconds, truncated, so a commit made
// just after the snapshot can carry a date up to a second earlier, and clocks disagree a little.
// Two seconds covers both. The cost: a commit made in the two seconds before the snapshot counts
// too, so if the command also fetches such a commit, it is credited.
const committerSlack = 2 * time.Second

// Ref is a worktree's HEAD, plus what Head saw of the repository's refs and when it looked. SHA
// is empty before the first commit; Branch is empty when detached. Time is when Head started
// reading. Keep the whole value: NewCommits needs the tips and Time, and it refuses a Ref whose
// Time is zero rather than crediting every commit reachable from after.
type Ref struct {
	SHA    string
	Branch string
	Time   time.Time
	tips   []string
}

// Commit is a newly reachable commit. Branch is the after snapshot's branch, since git commits
// themselves have no branch membership; it is empty for a detached HEAD.
type Commit struct {
	SHA     string
	Subject string
	Branch  string
}

// Head reads this worktree's own HEAD, including in linked worktrees, and records every ref tip
// (local branches, remote-tracking branches and tags) with the time it started. Its for-each-ref
// scan costs a little per call, and so does a commit peel for each annotated tag.
func Head(ctx context.Context, worktree string) (Ref, error) {
	// Read the clock before any ref: a commit made after this and not on a tip is one the command made.
	ref := Ref{Time: time.Now()}
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
	seen := make(map[string]bool)
	addTip := func(sha string) {
		if !seen[sha] {
			seen[sha] = true
			ref.tips = append(ref.tips, sha)
		}
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
			addTip(fields[2])
		case "tag":
			// Peel recursively, including tags of tags; tags of trees/blobs have no history.
			sha, err := gitx.RunIn(ctx, worktree, "rev-parse", "--verify", "--quiet", "--end-of-options", fields[2]+"^{commit}")
			if err != nil {
				if exitCode(err, 1) {
					continue
				}
				return Ref{}, err
			}
			addTip(strings.TrimSpace(sha))
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

// NewCommits returns the commits a command made, oldest first in topological order. A commit is
// returned when all three hold:
//
//  1. it is reachable from after;
//  2. it is not reachable from any ref tip in before, whether or not the branch changed;
//  3. its committer date is no earlier than before.Time less committerSlack.
//
// Rule 2 keeps existing history out, including history a branch switch, merge, fast-forward or
// rebase brings in from a branch that already existed. Rule 3 keeps out history fetched by the
// command, such as a pull's teammate commits, which were committed before the command began.
// Amend, rebase, cherry-pick and pull --rebase all stamp a new committer date, so their results
// pass rule 3. A push in the same command does not hide a commit: rule 2 looks only at before.
//
// Known limits. A commit that another writer makes to this HEAD's history during the command, and
// that the same command fetches, passes all three rules and is credited. Writers that commit to
// other branches are not credited, because their commits are not reachable from after. A fetched
// commit whose committer clock runs far ahead also passes rule 3. A rewrite that reproduces an
// existing commit exactly (same parent, tree, author and committer second) yields that commit's
// SHA, which is a tip in before, so rule 2 drops it. All rare, and none handled.
func NewCommits(ctx context.Context, worktree string, before, after Ref) ([]Commit, error) {
	if before.Time.IsZero() {
		return nil, errors.New("attrib: before was not read by Head")
	}
	if after.SHA == "" {
		return nil, nil
	}
	args := []string{"log", "--reverse", "--topo-order", "--no-show-signature", "--encoding=UTF-8", "--format=%H%x00%ct%x00%s", "-z", "--end-of-options", after.SHA}
	if before.SHA != "" {
		args = append(args, "^"+before.SHA)
	}
	for _, tip := range before.tips {
		args = append(args, "^"+tip)
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
	if len(fields)%3 != 0 {
		return nil, fmt.Errorf("unexpected git log output: %q", out)
	}
	earliest := before.Time.Add(-committerSlack)
	for i := 0; i < len(fields); i += 3 {
		committed, err := strconv.ParseInt(fields[i+1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unexpected committer date in git log output: %q", fields[i+1])
		}
		if time.Unix(committed, 0).Before(earliest) {
			continue
		}
		commits = append(commits, Commit{SHA: fields[i], Subject: fields[i+2], Branch: after.Branch})
	}
	return commits, nil
}

func exitCode(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}
