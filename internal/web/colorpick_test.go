package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The harness prints which colours the page's Settings holds back as taken, and which it offers
// though another device picked them.
const colorHoldersHarness = `
const vm = require("vm");
const fs = require("fs");
const ctx = {};
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], "utf8") + ";this.colorHolders = colorHolders;", ctx);
const { taken, offline } = ctx.colorHolders([
  { id: "here", self: true, reachable: true, color: 1, picked: true },
  { id: "maya-mbp-91c0", nick: "maya", reachable: true, color: 8, picked: true },
  { id: "sam-desktop-07be", owner: "Sam", reachable: false, color: 4, picked: true },
  { id: "jo-xps-2b90", reachable: true, color: 2 },
]);
const show = (m) => [...m].map(([c, who]) => c + " " + who).join(", ");
console.log("taken: " + show(taken));
console.log("offline: " + show(offline));
`

// A device that died without a goodbye leaves its pick in presence, and its daemon can't let it
// go (#581). The page offers a colour picked by a device that isn't reachable, naming whose it
// was, and holds back only one a reachable device picked.
func TestPageOffersTheColourOfAnOfflineDevice(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	dir := t.TempDir()
	src, harness := filepath.Join(dir, "page.js"), filepath.Join(dir, "harness.js")
	for path, text := range map[string]string{src: pageFunctions(t, "colorHolders"), harness: colorHoldersHarness} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := exec.Command(node, harness, src).CombinedOutput()
	if err != nil {
		t.Fatalf("the harness: %v\n%s", err, b)
	}
	got := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	want := "taken: 8 maya\noffline: 4 Sam"
	if got != want {
		t.Errorf("the harness printed\n%s\nwant\n%s", got, want)
	}
}
