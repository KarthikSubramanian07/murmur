package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The harness runs the page's code for the desktop app (#23) with a stand-in window: murmurApp,
// when given, records what the page tells it.
const desktopAppHarness = `
const vm = require("vm");
const fs = require("fs");
const src = fs.readFileSync(process.argv[2], "utf8");
function run(withApp) {
  const told = [];
  const ctx = {
    NAME: "Murmur",
    ui: { state: { account: { name: "tyler", unread: [] } }, ids: new Set(), readLocal: new Set() },
    named: new Map(),
    location: { host: "127.0.0.1:9" },
    document: { hidden: false, hasFocus: () => true, documentElement: {} },
    getComputedStyle: () => ({ colorScheme: "dark" }),
    window: {},
  };
  if (withApp) ctx.window.murmurApp = (n) => { told.push(n); return Promise.resolve(); };
  vm.createContext(ctx);
  vm.runInContext(src + "\nlet toldApp = \"\";\nthis.tell = tellApp; this.notesOf = appNotes;", ctx);
  const msg = (id, from) => ({ id, from, fromAddr: "laptop/" + from, subject: "about " + id });
  const unread = (...ids) => { ctx.ui.state.account.unread = ids; for (const id of ids) ctx.ui.ids.add(id); };
  // Nothing new: the title and count, once.
  ctx.tell();
  ctx.tell();
  // Two arrive, one to the person; their own message and one that isn't theirs make no note.
  unread("m1");
  ctx.tell(ctx.notesOf([msg("m1", "quiet-otter"), msg("m2", "brave-heron"), msg("m3", "tyler")]));
  // Five arrive at once: one note sums them up.
  unread("m1", "a", "b", "c", "d");
  ctx.tell(ctx.notesOf(["a", "b", "c", "d"].map((id) => msg(id, "keen-pike"))));
  // Read in the window: the count goes, and the theme is light.
  unread();
  ctx.getComputedStyle = () => ({ colorScheme: "light" });
  ctx.tell();
  console.log(JSON.stringify(told));
}
run(false);
run(true);
`

// The page tells the desktop app its title, unread count and theme when they change, and the
// messages to the person as they arrive; in a browser, with no murmurApp, it tells nothing.
func TestPageTellsTheDesktopApp(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "page.js")
	if err := os.WriteFile(src, []byte(pageFunctions(t, "inApp", "tellApp", "appNotes", "unreadIds", "unreadCount", "nameOf", "fromName", "me", "myName")), 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harness, []byte(desktopAppHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(node, harness, src).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		`[]`,
		`[{"host":"127.0.0.1:9","title":"Murmur","unread":0,"notes":[],"seen":true,"dark":true},` +
			`{"host":"127.0.0.1:9","title":"(1) Murmur","unread":1,"notes":[{"title":"quiet-otter","body":"about m1"}],"seen":true,"dark":true},` +
			`{"host":"127.0.0.1:9","title":"(5) Murmur","unread":5,"notes":[{"title":"Murmur","body":"4 new messages"}],"seen":true,"dark":true},` +
			`{"host":"127.0.0.1:9","title":"Murmur","unread":0,"notes":[],"seen":true,"dark":false}]`,
	}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}
