package daemon

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// commitBig makes a commit with message msg, given on stdin, as a message too large for a command
// line is, and returns it.
func (r *giveUpRig) commitBig(parent, msg string) string {
	r.t.Helper()
	tree := r.git(r.author, "hash-object", "-t", "tree", "-w", "--stdin")
	args := []string{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit-tree", tree, "-F", "-"}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = r.author
	cmd.Stdin = strings.NewReader(msg)
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("commit-tree: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// A commit on a log over the size a reader reads is passed over unread, as junk is, and the cursor
// moves past it, so it isn't read again after a restart and what follows it is read as usual. Git
// keeps commits compressed, so one this large can come in a small fetch; read whole, it could run
// the daemon out of memory, on every start while the ref stays.
func TestALogCommitOverTheLimitIsPassedOver(t *testing.T) {
	r := newGiveUpRig(t)
	tip := r.append("", r.message("before"))
	r.sync()

	big := r.message("too large")
	big.Body = strings.Repeat("x", 2<<20) // a valid event, over the 1 MB a reader reads
	msg, err := bus.Encode(big, big.Line())
	if err != nil {
		t.Fatal(err)
	}
	tip = r.commitBig(tip, msg)
	r.git(r.author, "push", "-q", "--force", r.remote, tip+":"+giveUpRef)
	tip = r.append(tip, r.message("after"))
	r.sync()

	if got := r.queued(); !slices.Equal(got, []string{"after"}) {
		t.Errorf("queued %q, want what came after the large commit, and not it", got)
	}
	if got := r.d.st.Processed[giveUpRef]; got != tip {
		t.Errorf("processed %s, want the tip, %s, past the large commit", got, tip)
	}
}

// A long log, as a device back after days away reads, is read a page at a time: every event in it
// is read, in order, and the cursor ends at the tip.
func TestALongLogIsReadInPages(t *testing.T) {
	r := newGiveUpRig(t)
	r.d.repo.Page = 3
	tip := r.append("", r.message("read before")) // the first sync passes over a log's history
	r.sync()
	var want []string
	for i := range 10 {
		subject := "message " + string(rune('a'+i))
		tip = r.append(tip, r.message(subject))
		want = append(want, subject)
	}
	r.sync()

	var got []string
	r.d.mu.Lock()
	for _, m := range r.d.st.Inbox[proto.CLISession] {
		got = append(got, m.Event.Subject)
	}
	processed := r.d.st.Processed[giveUpRef]
	r.d.mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("the inbox holds %q, want %q in order", got, want)
	}
	if processed != tip {
		t.Errorf("processed %s, want the tip, %s", processed, tip)
	}
}

// A presence over the size a reader reads is taken for one it can't read: its device is alive, and
// nothing in it, a session's name included, is believed.
func TestAPresenceOverTheLimitIsUnreadable(t *testing.T) {
	r := newGiveUpRig(t)
	r.d.maxRecord = 16 << 10
	p := bus.Presence{V: bus.Version, Device: giveUpPeer, Host: strings.Repeat("h", 64<<10), At: time.Now().UTC(),
		Sessions: []bus.SessionInfo{{ID: "aaaaaa", Name: "quiet-otter", Agent: "claude", Started: time.Now().UTC()}}}
	msg, err := bus.Encode(p, "presence")
	if err != nil {
		t.Fatal(err)
	}
	sha := r.commitBig("", msg)
	r.git(r.author, "push", "-q", "--force", r.remote, sha+":"+brand.PresencePrefix+giveUpPeer)
	r.sync()

	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	peer := r.d.peers[giveUpPeer]
	if peer == nil {
		t.Fatal("the device isn't known")
	}
	if len(peer.pres.Sessions) != 0 || peer.pres.Host != "presence unreadable" {
		t.Errorf("the presence was read: %+v", peer.pres)
	}
	if _, ok := r.d.st.Used["quiet-otter"]; ok {
		t.Error("a name in the presence was taken for used")
	}
}
