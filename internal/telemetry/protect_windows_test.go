//go:build windows

package telemetry

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func wellKnown(t *testing.T, kind windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(kind)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

// fakeForeignOwner makes the process look as if it ran as Local Service.
func fakeForeignOwner(t *testing.T) {
	t.Helper()
	foreign := wellKnown(t, windows.WinLocalServiceSid)
	real := processSIDs
	processSIDs = func() ([]*windows.SID, error) { return []*windows.SID{foreign}, nil }
	t.Cleanup(func() { processSIDs = real })
}

// loosen gives Everyone full access, inherited from nothing.
func loosen(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

type aceView struct {
	sid       *windows.SID
	mask      windows.ACCESS_MASK
	flags     uint8
	inherited bool
}

func readDACL(t *testing.T, path string) (protected bool, aces []aceView) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("%s: ACE type %d", path, ace.Header.AceType)
		}
		aces = append(aces, aceView{
			sid:       (*windows.SID)(unsafe.Pointer(&ace.SidStart)),
			mask:      ace.Mask,
			flags:     ace.Header.AceFlags,
			inherited: ace.Header.AceFlags&windows.INHERITED_ACE != 0,
		})
	}
	return control&windows.SE_DACL_PROTECTED != 0, aces
}

func allowedSIDs(t *testing.T) []*windows.SID {
	t.Helper()
	sids, err := processSIDs()
	if err != nil {
		t.Fatal(err)
	}
	return []*windows.SID{sids[0], wellKnown(t, windows.WinLocalSystemSid), wellKnown(t, windows.WinBuiltinAdministratorsSid)}
}

func onlyAllowed(t *testing.T, aces []aceView) bool {
	t.Helper()
	allowed := allowedSIDs(t)
	for _, a := range aces {
		ok := false
		for _, s := range allowed {
			ok = ok || a.sid.Equals(s)
		}
		if !ok {
			return false
		}
	}
	return len(aces) > 0
}

func isLoose(t *testing.T, path string) bool {
	t.Helper()
	protected, aces := readDACL(t, path)
	return !protected || !onlyAllowed(t, aces)
}

func assertTight(t *testing.T, path string, dir bool) {
	t.Helper()
	protected, aces := readDACL(t, path)
	if !onlyAllowed(t, aces) {
		t.Errorf("%s: a foreign SID has access", path)
	}
	if dir && filepath.Base(path) == "telemetry" && !protected {
		t.Errorf("%s: DACL not protected", path)
	}
	if !dir || filepath.Base(path) != "telemetry" {
		// Children carry only what they inherit from the root.
		for _, a := range aces {
			if !a.inherited {
				t.Errorf("%s: explicit ACE for %s", path, a.sid)
			}
		}
	}
}

// Windows ignores the umask; nothing to set.
func withUmask(t *testing.T, mask int) {}

// plantReparse puts a directory junction at link.
func plantReparse(t *testing.T, target, link string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
}

func TestRootDACL(t *testing.T) {
	noOverride(t)
	root := filepath.Join(tempBase(t), "telemetry")
	r, p, err := openProtected(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// A fresh directory inherits its parent's DACL, so the first open
	// replaces it.
	if !p.ModeFixed {
		t.Errorf("protection %+v, want a reported DACL repair", p)
	}
	protected, aces := readDACL(t, root)
	if !protected || !onlyAllowed(t, aces) {
		t.Fatalf("root DACL protected=%v, aces=%d", protected, len(aces))
	}
	for _, a := range aces {
		if a.mask&fileAllAccess != fileAllAccess {
			t.Errorf("ACE for %s grants %#x, want full access", a.sid, a.mask)
		}
		if a.flags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			t.Errorf("ACE for %s is not inherited by children", a.sid)
		}
	}
	if p, err := checkRoot(root); err != nil || p.ModeFixed {
		t.Errorf("second check: %+v, %v", p, err)
	}

	f, err := openRegular(r, testSegment, os.O_WRONLY|os.O_CREATE)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, name := range []string{"v1", "v1/" + testID, testSegment} {
		assertTight(t, filepath.Join(root, filepath.FromSlash(name)), name != testSegment)
	}

	// A widened root is repaired and reported.
	loosen(t, root)
	if p, err := checkRoot(root); err != nil || !p.ModeFixed {
		t.Fatalf("loosened root: %+v, %v", p, err)
	}
	assertTight(t, root, true)
}

func TestRootOwnerSID(t *testing.T) {
	noOverride(t)
	path := tempBase(t)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureOwner(path, info); err != nil {
		t.Errorf("own directory refused: %v", err)
	}
	fakeForeignOwner(t)
	if err := secureOwner(path, info); err == nil {
		t.Error("foreign SID accepted")
	}
}
