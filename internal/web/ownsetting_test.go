package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The harness runs the page's own wakeNever and withOwn for a session that was set to Never by its
// address while it had no name, and has a name now.
const ownSettingHarness = `
const vm = require("vm");
const fs = require("fs");
const src = fs.readFileSync(process.argv[2], "utf8");
const ctx = { ui: { settings: { wake: { when: "always" }, sessionWake: { "dev/aaaaaa": { when: "never" } } } } };
vm.createContext(ctx);
vm.runInContext(src + "\nthis.wakeNever = wakeNever; this.withOwn = withOwn;", ctx);
const self = { self: true };
const named = { name: "quiet-otter", addr: "dev/aaaaaa" };
const other = { name: "brave-heron", addr: "dev/bbbbbb" };
console.log("named now: " + ctx.wakeNever(self, named) + ", another: " + ctx.wakeNever(self, other));
console.log("saved: " + JSON.stringify(ctx.withOwn(ctx.ui.settings.sessionWake, named, { when: "within", minutes: 5 })));
console.log("cleared: " + JSON.stringify(ctx.withOwn(ctx.ui.settings.sessionWake, named, null)));
`

// A session set to Never while it had no name, by its address as the page showed it, still shows as
// set so once its name lands; saving its setting again keeps it by the name, and drops the one by
// the address, as does clearing it (#43).
func TestASettingMadeBeforeTheNameLandedShowsAfterIt(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "page.js")
	if err := os.WriteFile(src, []byte(pageFunctions(t, "wakeNever", "ownSetting", "withOwn")), 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harness, []byte(ownSettingHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(node, harness, src).CombinedOutput()
	if err != nil {
		t.Fatalf("the harness: %v\n%s", err, b)
	}
	got := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	want := strings.Join([]string{
		"named now: true, another: false",
		`saved: {"quiet-otter":{"when":"within","minutes":5}}`,
		"cleared: {}",
	}, "\n")
	if got != want {
		t.Errorf("the harness printed\n%s\nwant\n%s", got, want)
	}
}
