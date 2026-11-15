package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The harness runs the page's own fetchMessages against a stand-in api: two messages from two
// devices with one ID, then the first again, as the daemon gives it once more after reading its
// log back.
const msgKeyHarness = `
const vm = require("vm");
const fs = require("fs");
const src = fs.readFileSync(process.argv[2], "utf8");
const at = "2026-11-15T09:00:00Z";
const pages = [
  { cursor: "c1", messages: [
    { id: "m1", fromAddr: "laptop-0001/aaaaaa", from: "quiet-otter", to: "*", toKind: "all", subject: "first", at },
    { id: "m1", fromAddr: "desk-0002/bbbbbb", from: "brave-heron", to: "*", toKind: "all", subject: "same ID, another device", at },
  ] },
  { cursor: "c2", messages: [
    { id: "m1", fromAddr: "laptop-0001/aaaaaa", from: "quiet-otter", to: "*", toKind: "all", subject: "first", at },
  ] },
  { cursor: "c2", messages: [] },
];
const ctx = {
  ui: { ids: new Set(), messages: [], cursor: "" },
  tokenEra: 0, arrivedAt: 0, personNames: new Set(), learn: () => {},
  api: async () => pages.shift() || { cursor: "c2", messages: [] },
};
vm.createContext(ctx);
vm.runInContext(src + "\nthis.fetchMessages = fetchMessages;", ctx);
(async () => {
  await ctx.fetchMessages(0);
  await ctx.fetchMessages(0);
  console.log(ctx.ui.messages.map((m) => m.subject).join(" | "));
})().catch((e) => { console.error(e); process.exit(1); });
`

// Each device picks its own messages' IDs, so another device's message can carry an ID the page
// already has. The page lists both, and the same message given twice once (#43).
func TestAnIDAnotherDeviceRepeatsHidesNoMessageOnThePage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "page.js")
	if err := os.WriteFile(src, []byte(pageFunctions(t, "when", "msgKey", "fetchMessages")), 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harness, []byte(msgKeyHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(node, harness, src).CombinedOutput()
	if err != nil {
		t.Fatalf("the harness: %v\n%s", err, b)
	}
	if got, want := strings.TrimSpace(string(b)), "first | same ID, another device"; got != want {
		t.Errorf("the page lists %q, want %q", got, want)
	}
}
