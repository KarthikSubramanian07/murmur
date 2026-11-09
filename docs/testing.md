# Testing

`go test ./...` runs everything, including tests that run two devices against a local bare remote.
A change is checked with `gofmt -l .` (empty), `go vet ./...` and `go test -race -count=1 ./...`.
The `daemon` package is most of the time. Under `-race` on CI it took 460 s on Linux, 757 s on macOS
and 1,389 s on Windows, before any of its tests ran in parallel (2026-11-09; #39 is about making it
faster). Since then the tests that share nothing with the others call `t.Parallel()`, which took
the package from 750–872 s to 585–589 s on an 8-core laptop shared with other work (2026-11-09).

Go runs a package's serial tests first, one at a time, and the parallel ones after them, so the
two never overlap. A daemon test stays serial if it sets the environment (`t.Setenv` panics in a
parallel test; `useFakeT3` calls it), replaces a package variable, writes into the `HOME` or
`CODEX_HOME` that `TestMain` sets, or counts on timing or a port number that the other tests' load
could upset; those last say why in a comment. As of 2026-11-09 that is 77 of 271 tests, 50 of them
only because the stand-in `t3` is configured through the environment, which is #39's next step.
Run one at a time, the serial tests were about 45% of the package's time.

A package whose tests can reach Murmur's `murmur.*` git config calls `testgit.Isolate` in `TestMain`,
so a developer's own settings don't change what they see (#262). As of 2026-11-09 those are `update`,
`publicrepo`, `setup`, `daemon` and `cmd/mur`; the others that reach it passed with all three keys
set.

## On every pull request

`.github/workflows/test.yml` runs those checks on every pull request, on a push to `main`, and by
hand. Three jobs run them at once, `test-macos`, `test-windows` and `test-linux`, all on GitHub's
own runners, which are free for a public repo and start fresh for every job. A pull request from a
fork runs on them the same way.

Until 2026-11-01 some jobs ran on self-hosted machines, because GitHub's runners didn't start on
this account while the repo was private: the macOS job on a machine of Tyler's that also did other
work (until 2026-10-31), and the Linux job on a Spot VM in Google Cloud (project `t4-ci-3690`) that
stopped itself when idle and was woken by a webhook. They went before the repo went public. A
self-hosted runner runs whatever code a pull request brings and isn't wiped between jobs, and
GitHub advises keeping them off public repositories. The VM's setup and its wake service, `ci/`,
were deleted then (#113) and are in git history.

- **To test a pushed branch without a pull request:** `gh workflow run test.yml --ref <branch>`.
  Then `gh run watch` follows it, and `gh run view --log-failed` shows what failed.
- **gofmt doesn't run on Windows.** A Windows checkout turns line endings into CRLF, which gofmt
  reports as unformatted; the other two jobs check it.
- **A newer push to the same pull request** cancels the run in progress.
- **A daemon test that gives up waiting** prints every goroutine's stack, as the daemons run in the
  test's process; `MURMUR_TEST_LOG=1` prints their logs too. That is how a `git` whose start never
  came back on a macOS runner was found (#217).

## Pull requests from forks

A fork's pull request runs the workflow file as the fork changed it, so its code runs on GitHub's
runners with a read-only token and no secrets. The repository setting "Approval for running fork
pull request workflows from contributors" is set to "Require approval for all external
contributors" (Settings > Actions > General), so a fork's run waits for a maintainer first; read
what it changes under `.github/` before approving.
