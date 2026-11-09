package daemon

import (
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// #251. A human on another device takes do-not-disturb off a session while that device's log can't
// be fetched, and a human on this device's page then puts it back. When the fetch lands, the older
// change doesn't take it off again.
func TestATagChangeReadLateLeavesANewerOne(t *testing.T) {
	r := newGiveUpRig(t)
	r.bobKnown()
	r.bobHas("do-not-disturb")
	read := r.append("", r.message("read before"))
	r.sync()
	r.append(read, r.tagsEvent(time.Now().Add(-30*time.Minute), "do-not-disturb"))
	recovered := r.failFetches()
	r.sync()

	if err := (pageSource{r.d}).SetTags(r.ctx, bus.Addr(r.d.device, "bbbbbb"), []string{"do-not-disturb"}, nil); err != nil {
		t.Fatal(err)
	}
	recovered()
	r.sync()
	if r.off("do-not-disturb") {
		t.Error("the change read late overwrote the newer one")
	}
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if !r.d.dndLocked("bbbbbb") {
		t.Error("the session isn't on do not disturb")
	}
}

// A click on this device's page is the newest thing there is, whatever a mark says: one read from
// a device whose clock ran ahead, kept from before times were capped, can't make the page ignore it.
func TestAClickHereStandsOverAMarkFromTheFuture(t *testing.T) {
	r := newGiveUpRig(t)
	r.bobKnown()
	r.bobHas("do-not-disturb")
	addr := bus.Addr(r.d.device, "bbbbbb")
	r.d.mu.Lock()
	err := r.d.humanTagsLocked("bbbbbb", nil, []string{"do-not-disturb"}, "maya", time.Now().Add(24*time.Hour))
	r.d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := (pageSource{r.d}).SetTags(r.ctx, addr, []string{"do-not-disturb"}, nil); err != nil {
		t.Fatal(err)
	}
	if r.off("do-not-disturb") {
		t.Error("the click didn't put the tag back")
	}
	if err := (pageSource{r.d}).SetTags(r.ctx, addr, nil, []string{"do-not-disturb"}); err != nil {
		t.Fatal(err)
	}
	if !r.off("do-not-disturb") {
		t.Error("the next click didn't take it off")
	}
}

// A tags event dated in the future, by a clock ahead or a forged commit, is taken as sent now, so it
// can't hold off every later change until that time comes.
func TestATagChangeFromTheFutureCountsAsNow(t *testing.T) {
	r := newGiveUpRig(t)
	r.bobKnown()
	r.bobHas("do-not-disturb")
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	remove := r.tagsEvent(time.Now().Add(24*time.Hour), "do-not-disturb")
	r.d.applyTagsEventLocked(remove)
	if m := r.d.st.Tags["quiet-otter"].Human["do-not-disturb"]; m.At.After(time.Now()) {
		t.Fatalf("the mark is dated %s, in the future", m.At)
	}
	add := r.tagsEvent(time.Now().Add(2*time.Minute), "x")
	add.Remove, add.Add = nil, []string{"do-not-disturb"}
	r.d.applyTagsEventLocked(add)
	if !r.d.dndLocked("bbbbbb") {
		t.Error("a later change was held off by one dated in the future")
	}
}
