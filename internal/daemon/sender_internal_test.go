package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/names"
	"github.com/a-Fig/murmur/internal/proto"
)

// A turn is written from what the receiving daemon knows of the sender the moment the handoff
// takes the message, and the handoff starts as soon as the message is queued, while the sync that
// brought it may still be reading the rest of what it fetched (#71). The sender's name landed in
// the registry, and its presence went out, before its message did, so a sync that fetches the
// message fetches them too, and reads them first: the turn has the name and the agent.
//
// Here the peer's device is known from an earlier sync, without the session; then its session takes
// a name, shows in its presence, still unnamed there (as a presence pushed before the name landed
// is), and sends, all before the next sync. The handoff is played at the kick that queuing the
// message gives it, as pushLoop does.
func TestATurnKnowsTheSenderFetchedWithItsMessage(t *testing.T) {
	d, peer, _ := rateRig(t, rateOptions(), false)
	ctx := context.Background()
	d.mu.Lock()
	d.sessions["bbbbbb"].route = proto.Route{MsgSocket: "socket", MsgToken: "token"}
	d.mu.Unlock()
	const name = "dear-syrup"
	if !names.Valid(name) {
		t.Fatalf("%s isn't a name names.Generate could make", name)
	}
	push := func(ref string, v any, subject string, mode bus.PushMode) {
		t.Helper()
		msg, err := bus.Encode(v, subject)
		if err != nil {
			t.Fatal(err)
		}
		sha, err := peer.NewCommit(ctx, "", msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := peer.Push(ctx, sha, ref, mode, ""); err != nil {
			t.Fatal(err)
		}
	}
	presence := func(sessions ...bus.SessionInfo) {
		t.Helper()
		pres := bus.Presence{V: bus.Version, Device: "peer-0001", Host: "peer", Owner: "smash", At: time.Now().UTC(),
			Sessions: append([]bus.SessionInfo{}, sessions...)}
		push(brand.PresencePrefix+"peer-0001", pres, "peer-0001: presence", bus.Force)
	}

	presence()
	if err := d.pollOnce(ctx); err != nil {
		t.Fatal(err)
	}

	rec := bus.NameRecord{V: bus.Version, Name: name, Device: "peer-0001", Session: "aaaaaa", At: time.Now().UTC()}
	push(brand.NamesRef, rec, rec.Subject(), bus.Force)
	presence(bus.SessionInfo{ID: "aaaaaa", Agent: "codex", Started: time.Now().UTC()})
	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "peer-0001/aaaaaa", At: time.Now().UTC(),
		To: bus.Addr(d.device, "bbbbbb"), Subject: "auth middleware?", Body: "Are you changing it?"}
	push(brand.MeshPrefix+"peer-0001", e, e.Line(), bus.FastForward)

	select {
	case <-d.pushKick: // whatever kicked before the message
	default:
	}
	turn := make(chan string, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-d.pushKick:
			}
			wake := d.wakeSnapshot()
			d.mu.Lock()
			job := d.nextPushLocked("bbbbbb", wake)
			d.mu.Unlock()
			if job != nil {
				turn <- job.text
				return
			}
		}
	}()
	if err := d.pollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var got string
	select {
	case got = <-turn:
	case <-time.After(30 * time.Second):
		t.Fatal("the message was never queued for a handoff")
	}
	want := "Message from " + name + " (codex, on smash's machine) via " + brand.Product + ":\n\n" +
		"auth middleware?\n\nAre you changing it?\n\n" +
		"Reply with " + brand.Product + "'s `send` to " + name + " if a reply is needed.\n"
	if got != want {
		t.Errorf("the turn reads:\n%s\nwant:\n%s", got, want)
	}
}
