package bus_test

import (
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

const sealRef = "refs/murmur/mesh/dev-a"

func sealedEvent(t *testing.T) (string, bus.Event) {
	t.Helper()
	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "dev-a/s1", At: time.Now().UTC(),
		To: "dev-b/s2", Subject: "hello", Body: "line one\nline two"}
	msg, err := bus.EncodeSigned(e, e.Line(), bus.NewKey(), "dev-a", sealRef, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	return msg, e
}

// A sealed record opens for the ref and parent it was written for, and decodes as it always did, so
// a daemon from before seals reads it.
func TestSealOpensAndStillDecodes(t *testing.T) {
	msg, e := sealedEvent(t)
	state, seal := bus.Open(msg, sealRef, "abc123")
	if state != bus.Sealed || seal.By != "dev-a" {
		t.Fatalf("Open = %v %+v, want Sealed by dev-a", state, seal)
	}
	var got bus.Event
	if err := bus.Decode(msg, &got); err != nil || got.Body != e.Body || got.Subject != e.Subject {
		t.Fatalf("Decode = %+v, %v", got, err)
	}
}

// A seal binds the record to its ref and its parent: copied to another device's log, or pushed again
// after another commit, it no longer holds.
func TestSealBindsRefAndParent(t *testing.T) {
	msg, _ := sealedEvent(t)
	if s, _ := bus.Open(msg, "refs/murmur/mesh/dev-b", "abc123"); s != bus.BadSeal {
		t.Errorf("on another ref: %v, want BadSeal", s)
	}
	if s, _ := bus.Open(msg, sealRef, "def456"); s != bus.BadSeal {
		t.Errorf("after another parent: %v, want BadSeal", s)
	}
}

// Any change to what was signed breaks the seal, a field a reader doesn't know included.
func TestSealCoversEveryByte(t *testing.T) {
	msg, _ := sealedEvent(t)
	for _, c := range []struct{ name, from, to string }{
		{"body", `"subject":"hello"`, `"subject":"hellO"`},
		{"an added field", `{"v":1,`, `{"v":1,"x":2,`},
		{"the signer", `"by":"dev-a"`, `"by":"dev-c"`},
		{"something after the seal", "}\n", `,"x":1}` + "\n"},
	} {
		forged := strings.Replace(msg, c.from, c.to, 1)
		if forged == msg {
			t.Fatalf("%s: nothing replaced", c.name)
		}
		if s, _ := bus.Open(forged, sealRef, "abc123"); s != bus.BadSeal {
			t.Errorf("%s changed: %v, want BadSeal", c.name, s)
		}
	}
}

// A record with no seal, as every daemon before seals writes, opens as Unsealed, which the reader
// decides about.
func TestUnsealedRecord(t *testing.T) {
	msg, err := bus.Encode(bus.Event{V: bus.Version, Type: bus.SessionStopped}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := bus.Open(msg, sealRef, ""); s != bus.Unsealed {
		t.Errorf("Open = %v, want Unsealed", s)
	}
	if s, _ := bus.Open("junk", sealRef, ""); s != bus.Unsealed {
		t.Errorf("Open of junk = %v, want Unsealed", s)
	}
}
