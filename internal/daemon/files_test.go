package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// writeFile writes a file a test sends, in a directory of its own, and returns its full path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// inboxMessage waits for the one message in a session's inbox and returns it.
func inboxMessage(t *testing.T, d *device, sess string) proto.MessageView {
	t.Helper()
	var got []proto.MessageView
	eventually(t, "a message for "+sess, func() bool {
		got = append(got, d.do(proto.Request{Op: proto.OpInbox, Session: sess}).Messages...)
		return len(got) > 0
	})
	if len(got) != 1 {
		t.Fatalf("%d messages for %s, want one: %+v", len(got), sess, got)
	}
	return got[0]
}

// A message's files reach a session on another device as files on its own machine, byte for byte,
// saved inside the git directory, and the turn it reads gives their paths (#570). On the remote
// they sit encrypted on a ref of the message's own, which the receiver keeps no copy of.
func TestFilesTravelWithAMessage(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	var work, before string
	a := newDevice(t, remote)
	b := newDeviceWith(t, remote, func(d *device) {
		work = filepath.Dir(d.gitDir)
		before = git(t, work, "status", "--porcelain", "--ignored")
	})
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "claude-bob")
	eventually(t, "a to see bob", func() bool { return a.seesSession(bob) })
	eventually(t, "b to see alice", func() bool { return b.seesSession(alice) })

	image := append([]byte("\x89PNG\r\n\x1a\n\x00"), bytes.Repeat([]byte{0xfe, 0x00, 0x0d, 0x0a}, 5000)...)
	notes := []byte("first line\r\nsecond line\n")
	r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bob, Subject: "the failing page",
		Files: []string{writeFile(t, "shot one.png", image), writeFile(t, "notes.txt", notes)}})
	if r.Error != "" || r.Note != "" || len(r.Messages) != 1 {
		t.Fatalf("send: %+v", r)
	}
	id := r.Messages[0].ID

	// The hook's notice says there are files, and isn't taken for the whole message.
	var notices []proto.MessageView
	eventually(t, "a notice", func() bool {
		notices = b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages
		return len(notices) > 0
	})
	if text := view.Notices(notices, 1); !strings.Contains(text, `"the failing page", with 2 files`) {
		t.Errorf("notice: %q", text)
	}
	m := inboxMessage(t, b, "bbbbbb")
	if len(m.Files) != 2 || m.Files[0].Name != "shot_one.png" || m.Files[1].Name != "notes.txt" {
		t.Fatalf("files: %+v", m.Files)
	}
	for i, want := range [][]byte{image, notes} {
		f := m.Files[i]
		got, err := os.ReadFile(f.Path)
		if err != nil || !bytes.Equal(got, want) || f.Size != int64(len(want)) {
			t.Errorf("%s: %d bytes at %q, %v; want %d", f.Name, len(got), f.Path, err, len(want))
		}
		if !strings.HasPrefix(f.Path, b.gitDir+string(filepath.Separator)) {
			t.Errorf("%s is saved outside the git directory, at %s", f.Name, f.Path)
		}
	}
	turn := view.Turn(m)
	if !strings.Contains(turn, "Files, saved on this machine:\n- "+m.Files[0].Path+" (20 KB)\n- "+m.Files[1].Path+" (24 bytes)\n") {
		t.Errorf("turn:\n%s", turn)
	}
	if inbox := view.Inbox(proto.Response{Messages: []proto.MessageView{m}}); !strings.Contains(inbox, "- "+m.Files[1].Path+" (24 bytes)\n") {
		t.Errorf("inbox:\n%s", inbox)
	}

	// On the remote: one ref for the message, holding neither file in the clear, and the names
	// only inside the encrypted event.
	ref := "refs/murmur/files/" + a.self() + "/" + id
	if git(t, remote, "rev-parse", "--verify", "-q", ref) == "" {
		t.Fatalf("no %s on the remote", ref)
	}
	if tree := git(t, remote, "ls-tree", "--name-only", ref); tree != "0\n1" && tree != "0\r\n1" {
		t.Errorf("the files commit's tree: %q", tree)
	}
	if blob := git(t, remote, "cat-file", "blob", ref+":1"); strings.Contains(blob, "first line") {
		t.Error("the remote holds a file in the clear")
	}
	if log := git(t, remote, "log", "--format=%B", "refs/murmur/mesh/"+a.self()); strings.Contains(log, "notes.txt") {
		t.Error("the sender's log names a file in the clear")
	}
	if !strings.Contains(busText(t, remote, a), `"name":"notes.txt"`) {
		t.Error("the event doesn't list the file")
	}
	// The receiver's checkout and refs are as they were, and so is the sender's copy for its page.
	if after := git(t, work, "status", "--porcelain", "--ignored"); after != before {
		t.Errorf("the receiver's working tree changed:\n%s", after)
	}
	if refs := git(t, work, "for-each-ref", "refs/murmur/files"); refs != "" {
		t.Errorf("the receiver kept a files ref: %s", refs)
	}
	if got, err := os.ReadFile(filepath.Join(a.gitDir, "murmur", "files", id, "notes.txt")); err != nil || !bytes.Equal(got, notes) {
		t.Errorf("the sender's copy: %q, %v", got, err)
	}
}

// A session on the sender's own device has the files at once, with nothing fetched.
func TestFilesForASessionOnTheSameDevice(t *testing.T) {
	t.Parallel()
	a := newDevice(t, newRemote(t))
	a.session("aaaaaa", "")
	bob, _ := a.session("bbbbbb", "")
	log := []byte("panic: runtime error\n")
	if r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bob, Subject: "the log", Files: []string{writeFile(t, "run.log", log)}}); r.Error != "" {
		t.Fatalf("send: %s", r.Error)
	}
	m := inboxMessage(t, a, "bbbbbb")
	if len(m.Files) != 1 {
		t.Fatalf("files: %+v", m.Files)
	}
	if got, err := os.ReadFile(m.Files[0].Path); err != nil || !bytes.Equal(got, log) {
		t.Errorf("the file: %q, %v", got, err)
	}
}

// What a send's files may be: full paths of files, within the limits, with no secret in a text
// one. A refused send writes nothing to the remote.
func TestFilesASendRefuses(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDevice(t, remote)
	a.session("aaaaaa", "")
	small := writeFile(t, "small.txt", []byte("fine"))
	var nine []string
	for range bus.MaxFiles + 1 {
		nine = append(nine, small)
	}
	big := writeFile(t, "big.bin", make([]byte, bus.MaxFile+1))
	half := writeFile(t, "half.bin", make([]byte, bus.MaxFile-1))
	for what, c := range map[string]struct {
		files []string
		why   string
	}{
		"a path that isn't full":    {[]string{"small.txt"}, "isn't a full path"},
		"a file that isn't there":   {[]string{filepath.Join(t.TempDir(), "nothing.txt")}, "can't read"},
		"a directory":               {[]string{t.TempDir()}, "isn't a file"},
		"too many files":            {nine, "at most 8 files"},
		"a file over the limit":     {[]string{big}, "at most 10 MB"},
		"files over the limit":      {[]string{half, half, half}, "at most 20 MB together"},
		"a key in a text file":      {[]string{small, writeFile(t, "env.txt", []byte("AWS=AKIAIOSFODNN7EXAMPLE\n"))}, "env.txt"},
		"a key in a file named png": {[]string{writeFile(t, "shot.png", []byte("AKIAIOSFODNN7EXAMPLE"))}, "shot.png"},
	} {
		r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: "*", Subject: "files", Files: c.files})
		if !strings.Contains(r.Error, "not sent") || !strings.Contains(r.Error, c.why) {
			t.Errorf("%s: %q, want it not sent, saying %q", what, r.Error, c.why)
		}
	}
	if refs := git(t, remote, "for-each-ref", "refs/murmur/files"); refs != "" {
		t.Errorf("a refused send left files on the remote: %s", refs)
	}
}

// A message whose files can't be had still arrives, saying which file isn't there: a files ref
// that was never pushed, and one that holds something else. Anyone with push access can write
// either.
func TestMessageArrivesWithoutFilesThatCannotBeHad(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	b := newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	_, f, err := bus.SealFile([]byte("what the event describes"), "00000000000000aa", 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "gone.txt"
	forger := "forger-0001"
	pushEvent(t, remote, "refs/murmur/mesh/"+forger, bus.Event{V: bus.Version, ID: "00000000000000aa", Type: bus.Message,
		From: forger + "/cccccc", At: time.Now().UTC(), To: bob, Subject: "no ref", Files: []bus.File{f}})
	m := inboxMessage(t, b, "bbbbbb")
	if m.Subject != "no ref" || len(m.Files) != 1 || m.Files[0].Path != "" {
		t.Fatalf("message: %+v", m)
	}
	if turn := view.Turn(m); !strings.Contains(turn, "- gone.txt (24 bytes): not here.") {
		t.Errorf("turn:\n%s", turn)
	}

	// A ref that holds another blob at the file's place.
	work := t.TempDir()
	git(t, work, "init", "-q")
	blob := gitIn(t, work, "not the file", "hash-object", "-w", "--stdin")
	tree := gitIn(t, work, "100644 blob "+blob+"\t0\n", "mktree")
	commit := gitIn(t, work, "files", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit-tree", tree)
	git(t, work, "push", "-q", remote, commit+":refs/murmur/files/"+forger+"/00000000000000bb")
	f.Name = "swapped.txt"
	pushEvent(t, remote, "refs/murmur/mesh/"+forger, bus.Event{V: bus.Version, ID: "00000000000000bb", Type: bus.Message,
		From: forger + "/cccccc", At: time.Now().UTC(), To: bob, Subject: "wrong blob", Files: []bus.File{f}})
	m = inboxMessage(t, b, "bbbbbb")
	if m.Subject != "wrong blob" || len(m.Files) != 1 || m.Files[0].Path != "" {
		t.Fatalf("message: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(b.gitDir, "murmur", "files", "00000000000000bb", "swapped.txt")); err == nil {
		t.Error("a blob that isn't the file was saved as it")
	}
}

// A sender takes a message's files off the remote once they are past their time, and its own
// copies with them.
func TestFilesLeaveTheRemote(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDevice(t, remote)
	a.session("aaaaaa", "")
	r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: "*", Subject: "for a while", Files: []string{writeFile(t, "a.txt", []byte("a"))}})
	if r.Error != "" {
		t.Fatalf("send: %s", r.Error)
	}
	ref := "refs/murmur/files/" + a.self() + "/" + r.Messages[0].ID
	copies := filepath.Join(a.gitDir, "murmur", "files", r.Messages[0].ID)
	if _, err := os.Stat(copies); err != nil || gitOK(remote, "rev-parse", "--verify", "-q", ref) == "" {
		t.Fatalf("after the send: the copies %v, the ref %q", err, gitOK(remote, "rev-parse", "--verify", "-q", ref))
	}
	// The same device, with a time to keep files that has already passed.
	a.fileKeep, a.filePrune = time.Millisecond, 300*time.Millisecond
	a.restart()
	eventually(t, "the files ref to leave the remote", func() bool {
		return gitOK(remote, "rev-parse", "--verify", "-q", ref) == ""
	})
	eventually(t, "the sender's copy to go", func() bool {
		_, err := os.Stat(copies)
		return os.IsNotExist(err)
	})
}
