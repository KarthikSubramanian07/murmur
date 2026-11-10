package proc

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// aclOf is who the access list on path lets in, as SID strings, and whether the list is protected
// from its parent's.
func aclOf(t *testing.T, path string) (sids map[string]bool, protected bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the access list of %s: %v", path, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("%s has no access list: %v", path, err)
	}
	sids = map[string]bool{}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sids[(*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()] = true
	}
	return sids, control&windows.SE_DACL_PROTECTED != 0
}

// A state directory under a folder every user may read, as a clone outside the user's profile is,
// lets in only this user, SYSTEM and the Administrators once protected, and so does a file that was
// in it already, as an older version left it.
func TestProtectKeepsOtherUsersOut(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	me := user.User.Sid.String()
	const everyone, users, system, admins = "S-1-1-0", "S-1-5-32-545", "S-1-5-18", "S-1-5-32-544"

	// A parent that lets everyone in, and passes that on.
	parent := t.TempDir()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + me + ")(A;OICI;FR;;;WD)(A;OICI;FR;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	open, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, open, nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, ".git", "murmur")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "daemon.json")
	if err := os.WriteFile(old, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sids, _ := aclOf(t, old); !sids[everyone] {
		t.Fatalf("the test's parent didn't pass everyone on: %v", sids)
	}

	for range 2 { // a second start finds it protected already
		if err := Protect(dir); err != nil {
			t.Fatal(err)
		}
	}
	made := filepath.Join(dir, "state.json")
	if err := os.WriteFile(made, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{me: true, system: true, admins: true}
	for _, path := range []string{dir, old, made} {
		sids, protected := aclOf(t, path)
		if path == dir && !protected {
			t.Errorf("%s still takes its parent's access list", path)
		}
		for sid := range sids {
			if !want[sid] {
				t.Errorf("%s lets in %s", path, sid)
			}
		}
		if !sids[me] {
			t.Errorf("%s doesn't let this user in: %v", path, sids)
		}
		if sids[everyone] || sids[users] {
			t.Errorf("%s lets every user in: %v", path, sids)
		}
	}
	// GitHub's Windows runners run elevated, where the Administrators entry alone would let this
	// read in; the checks on the list above are what show the user's own entry is there.
	if b, err := os.ReadFile(old); err != nil || string(b) != "{}" {
		t.Errorf("reading %s once protected: %q, %v", old, b, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 2 {
		t.Errorf("Protect's probe file left behind, or the directory unreadable: %v, %v", entries, err)
	}
}

// A share checks access as the account its server knows, so a list naming this user's SID could
// shut them out there: Protect leaves a network path alone, and one it can't place, and sets the
// list on a local one.
func TestProtectLeavesNetworkDrivesAlone(t *testing.T) {
	if why := noACLs(t.TempDir()); why != "" {
		t.Errorf("a local temporary directory: %s", why)
	}
	for _, path := range []string{`\\localhost\C$\no-such-murmur-dir\.git\murmur`, `\\no-such-host.invalid\share\repo\.git\murmur`} {
		if why := noACLs(path); why == "" {
			t.Errorf("%s would be given the list", path)
		}
	}
	// Only a path that can't exist goes to Protect itself, so a mistake here touches nothing.
	if err := Protect(`\\no-such-host.invalid\share\repo\.git\murmur`); err == nil {
		t.Errorf("Protect reported it set the list on a share that doesn't exist")
	}
}
