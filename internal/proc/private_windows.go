package proc

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// Protect gives dir an access list of its own that lets in only this user, SYSTEM and the
// Administrators, and that what is in it inherits. On Windows a directory's mode bits do nothing:
// Go's 0o700 sets no access list, so .git\murmur took its parent's, and a clone outside the user's
// profile (C:\work, D:\src, a share) lets every user on the machine read it. daemon.json there
// holds the sessions' token, and a token holder can name the program the daemon runs to hand a
// message to T3, so reading the file is running code as this user.
//
// The list is protected, so nothing from the parent comes in, and setting it rewrites the inherited
// entries of the files already there, which tightens a directory an older version made. The user is
// named by their SID, not as the owner (OW): a file made by an elevated process is owned by the
// Administrators group, and an owner entry would follow that and leave the user out of their own
// files.
func Protect(dir string) error {
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
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("setting who may open %s: %w", dir, err)
	}
	return nil
}
