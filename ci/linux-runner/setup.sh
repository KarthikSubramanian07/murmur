#!/bin/bash
# Sets up the Linux CI runner for a-Fig/t4 on an Ubuntu VM (2026-10-30): git and a C compiler, Go,
# the GitHub Actions runner's files, and the timer that stops the VM when the runner is idle. Run as
# root from a copy of this folder on the VM; running it again changes nothing. Registering the
# runner with GitHub is a separate step (docs/testing.md, "The Linux runner").
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q git curl jq build-essential   # gcc: go test -race needs cgo

GOV=go1.27.3
GOSHA=65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40
if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "$GOV "; then
  curl -fsSL "https://go.dev/dl/$GOV.linux-amd64.tar.gz" -o /tmp/go.tgz
  echo "$GOSHA  /tmp/go.tgz" | sha256sum -c -
  rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
fi
ln -sf /usr/local/go/bin/go /usr/local/bin/go
ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt

id runner >/dev/null 2>&1 || useradd -m -s /bin/bash runner
RV=2.338.2
if [ ! -f /home/runner/actions-runner/config.sh ]; then
  sudo -u runner mkdir -p /home/runner/actions-runner
  curl -fsSL -o /tmp/runner.tgz "https://github.com/actions/runner/releases/download/v$RV/actions-runner-linux-x64-$RV.tar.gz"
  sudo -u runner tar -C /home/runner/actions-runner -xzf /tmp/runner.tgz && rm /tmp/runner.tgz
  /home/runner/actions-runner/bin/installdependencies.sh
fi

install -m 0755 "$here/t4-idle-stop" /usr/local/sbin/t4-idle-stop
install -m 0644 "$here/t4-idle-stop.service" /etc/systemd/system/t4-idle-stop.service
install -m 0644 "$here/t4-idle-stop.timer" /etc/systemd/system/t4-idle-stop.timer
systemctl daemon-reload
systemctl enable --now t4-idle-stop.timer
go version
