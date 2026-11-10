package publicrepo

import (
	"context"
	"testing"
)

// Status tells the page what has become of the report from what the last look left: a private repo
// as private, one whose report the site didn't store as waiting, and one reported as reported, with
// the address that went and never a credential from the remote's URL.
func TestStatusFollowsTheLooks(t *testing.T) {
	ctx := context.Background()
	f := newFakes(t)
	check := func(gitDir, url, version, want, wantAddr string) {
		t.Helper()
		got, addr, _ := Status(gitDir, url, version)
		if got != want || addr != wantAddr {
			t.Errorf("Status(%q, %q) = %q, %q; want %q, %q", url, version, got, addr, want, wantAddr)
		}
	}

	const url = "https://x-access-token:ghp_secret@github.com/acme/widget.git"
	gitDir := clone(t, remote(t), url)
	check(gitDir, url, "1.2.3", "waiting", "github.com/acme/widget")
	check(gitDir, url, "1.2.3-dev", "dev", "")
	check(gitDir, url, "", "dev", "")
	check(gitDir, "https://gitlab.com/acme/widget.git", "1.2.3", "not-github", "")

	f.public = false
	if _, err := Once(ctx, f.opts(gitDir, day1)); err != nil {
		t.Fatal(err)
	}
	check(gitDir, url, "1.2.3", "private", "github.com/acme/widget")
	if _, _, looked := Status(gitDir, url, "1.2.3"); looked != "2026-11-08" {
		t.Errorf("looked on %q, want 2026-11-08", looked)
	}

	f.public, f.siteDown = true, true
	other := clone(t, remote(t), url)
	if _, err := Once(ctx, f.opts(other, day1)); err == nil {
		t.Fatal("a report the site didn't store went through")
	}
	check(other, url, "1.2.3", "waiting", "github.com/acme/widget")
	f.siteDown = false
	if res, err := Once(ctx, f.opts(other, day1)); err != nil || res != Reported {
		t.Fatalf("site back: %v, %v", res, err)
	}
	check(other, url, "1.2.3", "reported", "github.com/acme/widget")
}
