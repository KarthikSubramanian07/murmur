package proc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Protect gives dir an access list of its own that lets in only this user, SYSTEM and the
// Administrators, and that what is in it inherits. On Windows a directory's mode bits do nothing:
// Go's 0o700 sets no access list, so .git\murmur took its parent's, and a clone outside the user's
// profile (C:\work, D:\src) lets every user on the machine read it. daemon.json there holds the
// sessions' token, and a token holder can name the program the daemon runs to hand a message to
// T3, so reading the file is running code as this user.
//
// The list is protected, so nothing from the parent comes in, and setting it rewrites the inherited
// entries of the files already there, which tightens a directory an older version made. The user is
// named by their SID, not as the owner (OW): a file made by an elevated process is owned by the
// Administrators group, and an owner entry would follow that and leave the user out of their own
// files.
//
// A network drive is left alone, and so is a volume that keeps no access lists (FAT, exFAT). A
// share checks access as the account the server knows, which may not be this SID, so the list
// could shut the user out of their own state for good. After the list is set, a file is made and
// removed in dir; if that is refused, the list dir had is put back.
func Protect(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if why := noACLs(abs); why != "" {
		return fmt.Errorf("%s left as it is: %s", abs, why)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("finding this user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		user.User.Sid.String()))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	before, err := windows.GetNamedSecurityInfo(abs, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("reading who may open %s: %w", abs, err)
	}
	err = windows.SetNamedSecurityInfo(abs, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("setting who may open %s: %w", abs, err)
	}
	if err := probe(abs); err != nil {
		restore(abs, before)
		return fmt.Errorf("%s left as it was: this user couldn't write in it with its new access list: %w", abs, err)
	}
	return nil
}

// noACLs says why the volume dir is on can't safely take the list, "" if it can.
func noACLs(dir string) string {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err.Error()
	}
	buf := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return "its volume isn't known: " + err.Error()
	}
	root := &buf[0]
	if windows.GetDriveType(root) == windows.DRIVE_REMOTE {
		return "it is on a network drive, where the server may not know this user's SID"
	}
	var flags uint32
	if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return "its volume isn't known: " + err.Error()
	}
	if flags&windows.FILE_PERSISTENT_ACLS == 0 {
		return "its volume keeps no access lists"
	}
	return ""
}

// probe makes and removes a file in dir.
func probe(dir string) error {
	f, err := os.CreateTemp(dir, ".protect-*")
	if err != nil {
		return err
	}
	name := f.Name()
	err = f.Close()
	return errors.Join(err, os.Remove(name))
}

// restore puts back the access list dir had before, protected or inherited as it was.
func restore(dir string, sd *windows.SECURITY_DESCRIPTOR) {
	dacl, _, err := sd.DACL()
	if err != nil {
		return
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control, _, err := sd.Control(); err == nil && control&windows.SE_DACL_PROTECTED != 0 {
		info = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil)
}
