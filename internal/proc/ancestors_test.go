package proc

import (
	"os"
	"testing"
)

// This process's ancestors start with itself and go on to its parent.
func TestAncestorsStartWithTheProcessAndItsParent(t *testing.T) {
	got := Ancestors(os.Getpid(), 4)
	if len(got) < 2 || got[0] != os.Getpid() || got[1] != os.Getppid() {
		t.Fatalf("Ancestors(%d) = %v; want it, then its parent %d", os.Getpid(), got, os.Getppid())
	}
	if n := len(Ancestors(os.Getpid(), 1)); n != 1 {
		t.Errorf("max 1 gave %d", n)
	}
}
