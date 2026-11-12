# Testing

`go test ./...` runs every Go test, including those that run two devices against a local bare remote.
A change is checked with `gofmt -l .` (empty), `go vet ./...` and `go test -race -count=1 ./...`.
The site has its own tests, of its Worker and of the files machines read on it (sitemap,
robots.txt, llms.txt, the pages' metadata): `node --test --import ./site/test/register.mjs
site/test/*.test.mjs`, with Node 20.6 or later and nothing to install. `test-linux` runs them.
The `daemon` package is most of the time (#39 is about making it faster). Its tests that share
nothing with the others call `t.Parallel()`. Under `-race` on CI that took the package from 464 s to
304 s on Linux, 798 s to 599 s on macOS and 1,500 s to 1,027 s on Windows (2026-11-09: main at
c9376fa, run 42689370600 in a-Fig/murmur, against the first run of the change, 42699090516 in the
contributor's fork, whose macOS job failed one test that has since been fixed). Windows takes
longest, so it decides when a pull request is green.

Go runs a package's serial tests first, one at a time, and the parallel ones after them, so the
two never overlap. A daemon test stays serial if it sets the environment (`t.Setenv` panics in a
parallel test), replaces a package variable, writes into the `HOME` or
`CODEX_HOME` that `TestMain` sets, or counts on timing or a port number that the other tests' load
could upset; those last say why in a comment. The stand-in `t3` is no reason to: `useFakeT3` puts
the test binary in the test's own directory, as a hard link or, on Windows, a copy, and the stand-in
reads its calls file and its fail and sleep settings from files beside it, not from the environment
(#282). That made 53 tests parallel. Three that use the stand-in stay serial:
`TestASessionWhoseMessageDidntFitGoesOnWithTheTurn` replaces a package variable, and
`TestDaemonLeavesOnceTheThreadIsNoLongerKept` and `TestDaemonWithAThreadToWakeStaysUpAndHandsOver`
time the daemon's own exit, which the first of them missed once when run in parallel.
`go test -race -count=1 ./internal/daemon` took 1,354 s and 1,199 s before the change and 1,013 s
and 1,015 s with 55 parallel, on an 8-core Mac that other work was loading heavily throughout
(2026-11-11, main at 3c275e4); with 53, on main at 6c1ca2e and as loaded, it took 1,352 s, so the
gain on a quiet machine is still to be measured, in CI's timings.

A parallel test meets more load than it did alone, so a wait it took for granted may come late. A
call's end, for one, is answered before its after-snapshot is taken: a test that commits right
after an end looks the session up first, since a lookup waits for the ends still being judged. And
a heartbeat can come later than the tests' 3 s stale limit: `who` still lists a stale device, but a
send to its sessions says nobody may read it. `seesSession` and `nameIs` wait for the device to be
fresh as well as listed, and `listsSession` is for waiting until a session is gone (#387).

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
