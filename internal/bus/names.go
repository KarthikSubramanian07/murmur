package bus

import (
	"fmt"
	"time"

	"github.com/a-Fig/t4/internal/names"
)

// NameRecord is one commit of the name registry, refs/t4/names. An assignment names the session
// that took Name. A snapshot, which a new root carries after the registry was lost or overwritten,
// lists every name its writer knew to be used, so that daemons joining later learn them all.
// Both can be in one commit.
//
// The client's own session ID is never written here: Session is the ID t4 gave the session.
type NameRecord struct {
	V       int       `json:"v"`
	Name    string    `json:"name,omitempty"`
	Device  string    `json:"device,omitempty"`
	Session string    `json:"session,omitempty"`
	At      time.Time `json:"at"` // the writer's clock; for display, never for ordering
	// Used maps each name in a snapshot to the address of the session that has it, or "" where
	// its writer didn't know.
	Used map[string]string `json:"used,omitempty"`
}

// Subject is the record as one readable line, the commit's subject.
func (r NameRecord) Subject() string {
	if r.Name == "" {
		return fmt.Sprintf("snapshot of %d names", len(r.Used))
	}
	return fmt.Sprintf("%s -> %s", r.Name, Addr(r.Device, r.Session))
}

// Claims returns the names a record read from the registry holds, each with its session's address
// ("" if unknown). Anyone with push access can write the registry, so a reader holds it to what
// t4 writes: only names that names.Generate could make, and addresses in their own alphabets.
func (r NameRecord) Claims() map[string]string {
	claims := map[string]string{}
	if r.V != Version {
		return claims
	}
	if names.Valid(r.Name) {
		addr := Addr(r.Device, r.Session)
		if !validAddr(addr) {
			addr = ""
		}
		claims[r.Name] = addr
	}
	for name, addr := range r.Used {
		if !names.Valid(name) {
			continue
		}
		if addr != "" && !validAddr(addr) {
			addr = ""
		}
		if _, ok := claims[name]; !ok {
			claims[name] = addr
		}
	}
	return claims
}
