package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The harness runs the page's own connBadge, with a stand-in h that keeps what it is given, for a
// page that lost its daemon a moment ago, one that lost it a minute ago, and one whose daemon is
// restarting onto an update, which keeps the page's address.
const connBadgeHarness = `
const vm = require("vm");
const fs = require("fs");
const src = fs.readFileSync(process.argv[2], "utf8");
const h = (tag, attrs, ...kids) => ({ attrs: attrs || {}, kids: kids.filter((k) => k) });
const ctx = { h, COMMAND: "mur", ui: { conn: "down", restarting: 0, downSince: 0 } };
vm.createContext(ctx);
vm.runInContext(src + "\nthis.connBadge = connBadge;", ctx);
const show = (label, ago, restarting) => {
  Object.assign(ctx.ui, { downSince: Date.now() - ago, restarting });
  const b = ctx.connBadge(false), m = ctx.connBadge(true);
  if (!b) return console.log(label + ": none");
  console.log(label + ": " + b.kids.filter((k) => typeof k === "string").join("") + " | " + b.attrs.title + " | mini " + m.attrs["aria-label"]);
};
show("just now", 1000, 0);
show("a minute", 60000, 0);
show("restarting", 60000, Date.now());
`

// A daemon that stopped and was started again serves the page at a new address with a new token,
// so a tab that lost its daemon never reaches it again on its own. Once it has been out of reach for
// a while, the badge says to run mur ui again, which opens the page where the daemon is now (#43).
func TestTheDisconnectedBadgeSaysWhatOpensThePageAgain(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "page.js")
	if err := os.WriteFile(src, []byte(pageFunctions(t, "DOWN_WAIT_MS", "connStuck", "connBadge")), 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harness, []byte(connBadgeHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(node, harness, src).CombinedOutput()
	if err != nil {
		t.Fatalf("the harness: %v\n%s", err, b)
	}
	got := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	want := strings.Join([]string{
		"just now: Disconnected | Can't reach the daemon. The page keeps trying. | mini Disconnected",
		"a minute: Run mur ui again | Can't reach the daemon. Started again, it serves the page at a new address, which mur ui opens. | mini Run mur ui again",
		"restarting: none",
	}, "\n")
	if got != want {
		t.Errorf("the harness printed\n%s\nwant\n%s", got, want)
	}
}
