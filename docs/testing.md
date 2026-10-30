# Testing

`go test ./...` runs everything, including tests that run two devices against a local bare remote.
A change is checked with `gofmt -l .` (empty), `go vet ./...` and `go test -race -count=1 ./...`.
The `daemon` package is most of the time. Under `-race` it took 10–16 minutes on a laptop shared
with other agents' test runs, and about 12 on the CI runner (2026-10-29; #39 is about making it
faster).

## On every pull request

`.github/workflows/test.yml` runs those checks on a pull request from a branch of this repo, on a
push to `main`, and by hand. Two jobs run them at once, each on a self-hosted runner: `test` on a
macOS machine (2 cores) that does other work too, set up 2026-10-29, and `test-linux` on a Linux VM
in Google Cloud, set up 2026-10-30 (below). Windows runs only on a developer's laptop.

- **To test a pushed branch without a pull request:** `gh workflow run test.yml --ref <branch>`.
  Then `gh run watch` follows it, and `gh run view --log-failed` shows what failed.
- **Low priority on macOS.** The runner service runs at background priority, each step under
  `taskpolicy -b nice -n 20`, with `GOMAXPROCS=2` and one package at a time.
- **Memory check on macOS.** A job won't start while the machine has less than 20% of its memory
  free, as `memory_pressure` reports it. Swap in use isn't the test: macOS keeps swap allocated long
  after memory frees up, and a swap threshold blocked every job for hours on 2026-10-29.
  The job then fails with a message saying so, and a re-run later works.
- **One job at a time on each runner,** so jobs queue, and a newer push to the same pull request
  cancels the run in progress.

## The Linux runner

A Spot VM in Google Cloud that runs nothing but the `test-linux` job. It stops itself when idle and
is started again when a job for it is queued, so it costs little while nobody pushes. Tyler gave it
a budget of $20 (2026-10-30); going past it is his to approve.

- **Where it is.** Project `t4-ci-3690`, on Tyler's billing account. The gcloud configuration
  `t4-ci` on his laptop points at it (`CLOUDSDK_ACTIVE_CONFIG_NAME=t4-ci`), and his default
  configuration is another project. The VM is `t4-ci-linux-1` in `us-central1-a`: e2-standard-4
  (4 vCPUs, 16 GB), Ubuntu 24.04, a 20 GB disk, no service account. The runner on it is
  `gcp-linux-1`, with the label `t4-ci-linux`.
- **Stopping.** A systemd timer runs `ci/linux-runner/t4-idle-stop` every 2 minutes. After 30
  minutes without a job it shuts the VM down. A stopped VM bills only its disk. A VM shutting itself
  down shows RUNNING until its system has stopped, so the script first marks it stopping, in the
  guest attribute `t4/stopping`. It then gives a job queued just before 90 seconds to reach the
  runner (GitHub gives a runner 60 to take a job), and stays up and deletes the mark if one does.
  If the mark can't be written, it tries again 2 minutes later, until the VM has been idle for 2
  hours; then it stops the VM anyway.
- **Starting.** The repo's webhook 705335778 sends every `workflow_job` event to the Cloud Run
  service `t4-ci-wake` (`ci/wake`). For a queued job that asks for `t4-ci-linux`, it starts the VM.
  If the VM is marked stopping, it first waits for the stop; a mark more than 10 minutes old is
  ignored. It then watches until the VM is RUNNING, and starts it again if a start failed, as a Spot
  VM's can when the zone has no capacity, for up to 5 minutes. The job waits in the queue
  until the VM has booted and the runner connects. The service can read, start and resume that one
  VM and read its guest attributes (custom role `t4CiWake`, granted on the instance). It can also
  read the project's operations (`t4CiWakeOps`), to learn whether a start worked.
- **Spot.** Google can stop a Spot VM at any time, with 30 seconds' notice. A job running then
  fails and needs a re-run, which queues a new job, so the VM starts again.
- **When a job stays queued.** Re-run it: the new job's webhook starts the VM. The webhook's
  deliveries are listed under Settings > Webhooks, and the service's log is
  `gcloud run services logs read t4-ci-wake --region us-central1`.
- **After 14 days unused,** GitHub deletes a runner that hasn't connected. The VM then boots to a
  runner that can't connect, and the runner has to be registered again (below).
- **Cost (estimates, 2026-10-30).** The VM costs about 4 cents an hour while it runs. The disk costs
  about $2 a month, running or not. The wake service fits in Cloud Run's free tier. The budget
  emails the billing account's admins at 50%, 90% and 100% of $20; a budget alerts, it doesn't stop
  spending.
- **No secrets on the VM.** The webhook's secret is in Cloud Run's environment and GitHub's webhook
  settings only; it was never written to a file. To change it, set the new one in both places:
  `gcloud run services update t4-ci-wake --region us-central1 --update-env-vars WEBHOOK_SECRET=…`
  and `gh api -X PATCH repos/a-Fig/t4/hooks/705335778/config -f secret=…`.
- **Not ephemeral.** A job can change files outside its checkout, such as the runner user's home
  directory and Go's caches, and the next job runs with them, as on the macOS runner. The VM has no
  service account, so a job can't reach the rest of the project.

### Setting it up again

With `CLOUDSDK_ACTIVE_CONFIG_NAME=t4-ci`, from the repo's root:

```sh
gcloud compute instances create t4-ci-linux-1 --zone=us-central1-a --machine-type=e2-standard-4 \
  --provisioning-model=SPOT --instance-termination-action=STOP \
  --image-family=ubuntu-2404-lts-amd64 --image-project=ubuntu-os-cloud \
  --boot-disk-size=20GB --boot-disk-type=pd-balanced --no-service-account --no-scopes \
  --metadata=enable-guest-attributes=TRUE --labels=purpose=t4-ci
gcloud compute scp --recurse ci/linux-runner t4-ci-linux-1:~ --zone=us-central1-a
gcloud compute ssh t4-ci-linux-1 --zone=us-central1-a --command='sudo bash linux-runner/setup.sh'
```

Then register the runner. `gh api -X POST repos/a-Fig/t4/actions/runners/registration-token --jq
.token` makes a token that lasts an hour. On the VM:

```sh
sudo -u runner bash -c 'cd ~/actions-runner && ./config.sh --unattended --replace \
  --url https://github.com/a-Fig/t4 --token TOKEN --name gcp-linux-1 --labels t4-ci-linux --work _work'
sudo bash -c 'cd /home/runner/actions-runner && ./svc.sh install runner && ./svc.sh start'
```

The wake service. A new project's default compute service account needed
`roles/cloudbuild.builds.builder` before `--source` deploys worked.

```sh
gcloud iam service-accounts create t4-ci-wake
gcloud iam roles create t4CiWake --project=t4-ci-3690 --permissions=\
compute.instances.get,compute.instances.start,compute.instances.resume,compute.instances.getGuestAttributes
gcloud compute instances add-iam-policy-binding t4-ci-linux-1 --zone=us-central1-a \
  --member=serviceAccount:t4-ci-wake@t4-ci-3690.iam.gserviceaccount.com \
  --role=projects/t4-ci-3690/roles/t4CiWake
gcloud iam roles create t4CiWakeOps --project=t4-ci-3690 --permissions=compute.zoneOperations.get
gcloud projects add-iam-policy-binding t4-ci-3690 \
  --member=serviceAccount:t4-ci-wake@t4-ci-3690.iam.gserviceaccount.com \
  --role=projects/t4-ci-3690/roles/t4CiWakeOps
gcloud run deploy t4-ci-wake --source ci/wake --region us-central1 \
  --service-account t4-ci-wake@t4-ci-3690.iam.gserviceaccount.com --allow-unauthenticated \
  --max-instances 1 --timeout 330 \
  --set-env-vars "WEBHOOK_SECRET=$SECRET,LABEL=t4-ci-linux,INSTANCE=projects/t4-ci-3690/zones/us-central1-a/instances/t4-ci-linux-1"
gh api -X POST repos/a-Fig/t4/hooks -f name=web -f "config[url]=$URL/" -f config[content_type]=json \
  -f "config[secret]=$SECRET" -f 'events[]=workflow_job' -F active=true
```

`$SECRET` is a new random value (`python -c "import secrets; print(secrets.token_hex(32))"`), kept
in a shell variable, and `$URL` is the service's URL from the deploy.

## Pull requests from forks

A pull request from a fork doesn't run the checks on either runner, because a job runs whatever code
a pull request contains, with the runner user's access to its machine. GitHub advises keeping
self-hosted runners off public repositories for this reason. Its author can run the checks above
locally. A maintainer can push the branch to this repo and open a pull request from it, or start a
run by hand.

That the job skips a fork's pull request is not by itself what keeps its code away. A pull request
from a fork runs the workflow file as the fork changed it, so it can delete the check, or add a
workflow of its own that asks for the same runners. What holds is the repository setting "Approval
for running fork pull request workflows from contributors", set to "Require approval for all
external contributors" (Settings > Actions > General). With it, a fork's workflow run waits for a
maintainer, and approving it runs the fork's code on the runners, so read what it changes under
`.github/` first. A fork's run gets a read-only token and no secrets either way.
