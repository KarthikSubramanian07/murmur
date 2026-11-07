package client

import (
	"path/filepath"
	"testing"
)

func TestUpdatedMCPStartsInstalledBinary(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"mur.exe.old-.mur-update-123", "mur.exe"},
		{"mur (deleted)", "mur"},
		{"mur.exe", "mur.exe"},
		{filepath.Join("dir.old-.mur-update-name", "mur.exe"), filepath.Join("dir.old-.mur-update-name", "mur.exe")},
	} {
		if got := installedExecutable(c.input); got != c.want {
			t.Errorf("installedExecutable(%q)=%q, want %q", c.input, got, c.want)
		}
	}
}
