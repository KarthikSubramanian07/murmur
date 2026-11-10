package testgit

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// RefusePushes makes the bare repository remote refuse the next times pushes to refs under prefix
// with "[remote rejected] (<reason>)", which is how GitHub refuses a push now and then as busy, and
// then take them as usual. With land, each refused push lands first, as a push that GitHub took
// but failed to report can. Refused reports how many pushes it has refused.
//
// git's own hooks can't refuse with a reason of their choosing ("pre-receive hook declined"), but
// a proc-receive hook (git 2.29) answers for each ref it is given, so this installs one.
func RefusePushes(t testing.TB, remote, prefix, reason string, times int, land bool) (refused func() int) {
	t.Helper()
	landing := ""
	if land {
		landing = `git update-ref "$3" "$2" || exit 1`
	}
	// pkt reads a pkt-line into $line and fails at a flush; say writes one.
	script := `#!/bin/sh
pkt() {
	len=$(dd bs=1 count=4 2>/dev/null)
	if [ -z "$len" ] || [ "$len" = 0000 ]; then return 1; fi
	line=$(dd bs=1 count=$((0x$len - 4)) 2>/dev/null)
}
say() { printf '%04x%s' $((${#1} + 4)) "$1"; }
while pkt; do :; done
say "version=1"
printf 0000
refs=
while pkt; do
	set -- $line
	` + landing + `
	refs="$refs $3"
done
n=$(($(cat refused 2>/dev/null || echo 0) + 1))
echo $n > refused
if [ $n -ge ` + strconv.Itoa(times) + ` ]; then git config --unset receive.procReceiveRefs; fi
for r in $refs; do say "ng $r ` + reason + `"; done
printf 0000
`
	if err := os.WriteFile(filepath.Join(remote, "hooks", "proc-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "--git-dir="+remote, "config", "receive.procReceiveRefs", prefix).CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	return func() int {
		b, err := os.ReadFile(filepath.Join(remote, "refused"))
		if errors.Is(err, fs.ErrNotExist) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			t.Fatalf("the count of refused pushes: %v", err)
		}
		return n
	}
}
