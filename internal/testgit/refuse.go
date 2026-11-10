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

// RefusePushes makes the bare repository remote refuse its next `times` pushes to refs under prefix
// with "[remote rejected] (<reason>)", which is how GitHub refuses a push now and then as busy, and
// then take pushes as usual. With land, each refused push lands first, as a push that GitHub took
// but reported as failed can. refused reports how many pushes it has refused.
//
// git's own hooks can't refuse with a reason of their choosing ("pre-receive hook declined"), but
// a proc-receive hook (git 2.29) answers for each ref it is given, so this installs one. It speaks
// git's pkt-lines (a 4-digit hex length, the length included, then the data; "0000" ends a list)
// with POSIX sh, dd and printf only, for dash and Git for Windows' sh alike.
func RefusePushes(t testing.TB, remote, prefix, reason string, times int, land bool) (refused func() int) {
	t.Helper()
	landing := ""
	if land {
		landing = `git update-ref "$3" "$2" || exit 1`
	}
	// size reads a pkt-line's length into $size and fails at a flush or the end; data reads the
	// rest of it into $line, skip throws it away, and say writes one.
	script := `#!/bin/sh
size() {
	size=$(dd bs=1 count=4 2>/dev/null)
	[ -n "$size" ] && [ "$size" != 0000 ]
}
data() { line=$(dd bs=1 count=$((0x$size - 4)) 2>/dev/null); }
skip() { dd bs=1 count=$((0x$size - 4)) >/dev/null 2>&1; }
say() { printf '%04x%s' $((${#1} + 4)) "$1"; }
while size; do skip; done
say "version=1"
printf 0000
refs=
while size; do
	data
	set -- $line
	` + landing + `
	refs="$refs $3"
done
count=$(cat refused 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > refused
if [ "$count" -ge ` + strconv.Itoa(times) + ` ]; then git config --unset receive.procReceiveRefs; fi
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
