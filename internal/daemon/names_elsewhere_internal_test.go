package daemon

import "testing"

// Whose a session name is goes by its device: the registry's, or an earlier sighting's, and else
// the device that set it aside (#604).
func TestNameElsewhere(t *testing.T) {
	t.Parallel()
	d := &Daemon{device: "here-0001", st: state{
		Used:     map[string]string{"quiet-otter": "peer-0001/aaaaaa", "brave-heron": "", "calm-finch": "here-0001/bbbbbb"},
		Reserved: map[string]string{"brave-heron": "peer-0002", "swift-lark": "peer-0001"},
	}}
	for _, c := range []struct {
		name, dev string
		want      bool
	}{
		{"quiet-otter", "peer-0001", false}, // its own, whichever of its sessions shows it (#37)
		{"quiet-otter", "peer-0002", true},
		{"calm-finch", "peer-0001", true},   // this device's
		{"brave-heron", "peer-0002", false}, // in use by nobody known: set aside by peer-0002
		{"brave-heron", "peer-0001", true},
		{"swift-lark", "peer-0001", false},
		{"swift-lark", "peer-0002", true},
		{"new-name", "peer-0002", false}, // nobody's yet: the first to show it has it
	} {
		d.mu.Lock()
		got := d.nameElsewhereLocked(c.name, c.dev)
		d.mu.Unlock()
		if got != c.want {
			t.Errorf("%s on %s: elsewhere = %v, want %v", c.name, c.dev, got, c.want)
		}
	}
}
