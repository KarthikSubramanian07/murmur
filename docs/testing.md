# Testing

`go test ./...` runs everything, including tests that run two devices against a local bare remote.
A change is checked with `gofmt -l .` (empty), `go vet ./...` and `go test -race -count=1 ./...`.
The `daemon` package is most of the time. Under `-race` it took 10–16 minutes on a laptop shared
with other agents' test runs, and about 12 on the CI runner (2026-10-29; #39 is about making it
faster).

## On every pull request

`.github/workflows/test.yml` runs those checks on a pull request from a branch of this repo, on a
push to `main`, and by hand. It runs on a self-hosted macOS runner (2 cores), set up 2026-10-29.

- **To test a pushed branch without a pull request:** `gh workflow run test.yml --ref <branch>`.
  Then `gh run watch` follows it, and `gh run view --log-failed` shows what failed.
- **Only macOS runs here.** Linux has never been run, and Windows only on a developer's laptop.
- **Low priority.** The runner machine does other work too. The runner service runs at background
  priority, each step under `taskpolicy -b nice -n 20`, with `GOMAXPROCS=2` and one package at a
  time.
- **Memory check.** A job won't start while more than 1.7 GB of the machine's 2 GB swap is in use.
  The job then fails with a message saying so, and a re-run later works.
- **One job at a time.** There is one runner, so jobs queue, and a newer push to the same pull
  request cancels the run in progress.

## Pull requests from forks

A pull request from a fork doesn't run the checks on that runner, because a job runs whatever code
a pull request contains, with the runner user's access to its machine. GitHub advises keeping
self-hosted runners off public repositories for this reason. Its author can run the checks above
locally. A maintainer can push the branch to this repo and open a pull request from it, or start a
run by hand.

That the job skips a fork's pull request is not by itself what keeps its code away. A pull request
from a fork runs the workflow file as the fork changed it, so it can delete the check, or add a
workflow of its own that asks for the same runner. What holds is the repository setting "Approval
for running fork pull request workflows from contributors", set to "Require approval for all
external contributors" (Settings > Actions > General). With it, a fork's workflow run waits for a
maintainer, and approving it runs the fork's code on the runner, so read what it changes under
`.github/` first. A fork's run gets a read-only token and no secrets either way.
