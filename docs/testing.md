# Testing

`go test ./...` runs everything, including tests that run two devices against a local bare remote.
A change is checked with `gofmt -l .` (empty), `go vet ./...` and `go test -race -count=1 ./...`.
The `daemon` package is most of the time. Under `-race` it took 10–16 minutes on Tyler's laptop
while other agents' tests ran there, and about 12 on the home server (2026-10-29; #39 is about
making it faster).

## On every pull request

`.github/workflows/test.yml` runs those checks on a pull request, on a push to `main`, and by hand.
It runs on a self-hosted runner, `homeserver`, set up 2026-10-29: Tyler's MacBook Pro home server
(macOS 12, Intel, 2 cores, 16 GB). GitHub's own runners wouldn't start on this account ("recent
account payments have failed or your spending limit needs to be increased"), and a runner on the
laptop would stop whenever Tyler puts the laptop to sleep.

- **To test a pushed branch without a pull request:** `gh workflow run test.yml --ref <branch>`.
  Then `gh run watch` follows it, and `gh run view --log-failed` shows what failed.
- **Only macOS runs here.** Linux has never been run, and Windows only on the laptop.

## The home server is shared

That machine also runs Tyler's production work: the Vi desktops in a colima VM, which held about
15 GB and 2 cores' worth of CPU on 2026-10-29, the heyvi services, and client ERP stacks. A test
run at normal priority would slow real users. So:

- **Low priority.** The runner's launchd service is `ProcessType Background` with `Nice 20`. Each
  step runs under `taskpolicy -b nice -n 20`, with `GOMAXPROCS=2` and one package at a time.
- **Memory check.** A job won't start while more than 1.7 GB of the machine's 2 GB swap is in use.
- **One job at a time.** There is one runner, so jobs queue, and a newer push to the same pull
  request cancels the run in progress.
- **What a job can do.** A job runs as Tyler's user on that machine, with whatever a pull request
  in this private repo contains. That is accepted because only Tyler's collaborators and their
  agents can open one.
- **Where it lives.** The runner is in `~/actions-runner`, and
  `~/Library/LaunchAgents/actions.runner.a-Fig-t4.homeserver.plist` starts it. `./svc.sh
  stop|start|uninstall` there controls it. To remove it for good, run `./config.sh remove` with a
  removal token from `gh api -X POST repos/a-Fig/t4/actions/runners/remove-token`.
