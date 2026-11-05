//go:build windows

package telemetry

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processSIDs are the owners a telemetry file may have: the token's user
// first, then the owner the token gives new files, which is Administrators
// when an administrator runs elevated. Tests fake a foreign user.
var processSIDs = func() ([]*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sids := []*windows.SID{user.User.Sid}
	if owner, err := tokenOwner(token); err == nil {
		sids = append(sids, owner)
	}
	return sids, nil
}

func tokenOwner(token windows.Token) (*windows.SID, error) {
	var n uint32
	windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &n)
	if n == 0 {
		return nil, errOwner
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buf[0], n, &n); err != nil {
		return nil, err
	}
	// TOKEN_OWNER holds one SID pointer into buf.
	return (*struct{ Owner *windows.SID })(unsafe.Pointer(&buf[0])).Owner.Copy()
}

func secureOwner(path string, info os.FileInfo) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return scrub(err)
	}
	return ownedBy(sd)
}

func ownedFile(f *os.File, _ os.FileInfo) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return scrub(err)
	}
	return ownedBy(sd)
}

func ownedBy(sd *windows.SECURITY_DESCRIPTOR) error {
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return errOwner
	}
	sids, err := processSIDs()
	if err != nil {
		return scrub(err)
	}
	for _, sid := range sids {
		if owner.Equals(sid) {
			return nil
		}
	}
	return errOwner
}

// handleInfo carries the link count, which only the handle reports.
type handleInfo struct {
	os.FileInfo
	links uint32
}

func fileInfo(f *os.File) (os.FileInfo, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var d windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &d); err != nil {
		return nil, err
	}
	return handleInfo{FileInfo: info, links: d.NumberOfLinks}, nil
}

// singleLink needs a handle's info; a path's info has no link count, so it
// never counts as one link.
func singleLink(info os.FileInfo) bool {
	h, ok := info.(handleInfo)
	return ok && h.links == 1
}

func unwrap(info os.FileInfo) os.FileInfo {
	if h, ok := info.(handleInfo); ok {
		return h.FileInfo
	}
	return info
}

// reparse catches junctions, mount points and every other reparse point,
// not only symlinks.
func reparse(info os.FileInfo) bool {
	d, ok := unwrap(info).Sys().(*syscall.Win32FileAttributeData)
	return ok && d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// fileAllAccess is FILE_ALL_ACCESS, the FA right in SDDL.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

// rootSDDL is a protected DACL: full access for the user, SYSTEM and
// Administrators, inherited by every file and directory below.
func rootSDDL(user *windows.SID) string {
	return "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.String() + ")"
}

func trustees() (user *windows.SID, allowed []*windows.SID, err error) {
	sids, err := processSIDs()
	if err != nil {
		return nil, nil, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, nil, err
	}
	return sids[0], []*windows.SID{sids[0], system, admins}, nil
}

// tightenRoot gives the root its protected DACL unless it already has it.
// Go ignores mode bits on Windows.
func tightenRoot(root string, _ os.FileInfo) (bool, error) {
	user, allowed, err := trustees()
	if err != nil {
		return false, scrub(err)
	}
	if protectedDACL(root, user, allowed) {
		return false, nil
	}
	sd, err := windows.SecurityDescriptorFromString(rootSDDL(user))
	if err != nil {
		return false, scrub(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false, errUnsafe
	}
	// Setting it also re-applies inheritance to everything below.
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return false, scrub(err)
	}
	return true, nil
}

func protectedDACL(root string, user *windows.SID, allowed []*windows.SID) bool {
	sd, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	hasUser := false
	ok := eachACE(sd, func(ace *windows.ACCESS_ALLOWED_ACE, sid *windows.SID) bool {
		const inherit = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
		if ace.Header.AceFlags&inherit != inherit || ace.Mask&fileAllAccess != fileAllAccess {
			return false
		}
		hasUser = hasUser || sid.Equals(user)
		for _, a := range allowed {
			if sid.Equals(a) {
				return true
			}
		}
		return false
	})
	return ok && hasUser
}

// eachACE calls fn for every ACE of the DACL; it is false when the DACL is
// missing or NULL, holds a non-allow ACE, or fn says no.
func eachACE(sd *windows.SECURITY_DESCRIPTOR, fn func(*windows.ACCESS_ALLOWED_ACE, *windows.SID) bool) bool {
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		if !fn(ace, (*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return false
		}
	}
	return true
}

// tighten leaves a child with only what it inherits from the root: an
// explicit or protected DACL is replaced by an empty, unprotected one.
func tighten(r *os.Root, name string, f *os.File, _ os.FileInfo) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return scrub(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		return scrub(err)
	}
	inherited := control&windows.SE_DACL_PROTECTED == 0 && eachACE(sd, func(ace *windows.ACCESS_ALLOWED_ACE, _ *windows.SID) bool {
		return ace.Header.AceFlags&windows.INHERITED_ACE != 0
	})
	if inherited {
		return nil
	}
	empty, err := windows.SecurityDescriptorFromString("D:")
	if err != nil {
		return scrub(err)
	}
	// Never a NULL DACL: that would grant everyone access.
	dacl, _, err := empty.DACL()
	if err != nil || dacl == nil {
		return errUnsafe
	}
	path := filepath.Join(r.Name(), filepath.FromSlash(name))
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return scrub(err)
	}
	return nil
}

// Windows offers no exclusion duolog can verify: Windows Search skips
// AppData by default, and OneDrive backup is not known to cover it. Doctor
// reports both as unverified.
func excludeBackup(string) (bool, error) { return false, nil }

func indexExcluded(string) bool { return false }

func refuseMount(string) error { return nil }
