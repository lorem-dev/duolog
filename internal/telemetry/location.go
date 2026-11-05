package telemetry

import (
	"errors"
	"path/filepath"
	"strings"
)

// Location holds the platform inputs the roots are resolved from. Callers
// fill it from runtime.GOOS and the environment; nothing here reads the
// environment, so there is no override of the roots.
type Location struct {
	GOOS         string
	Home         string
	XDGStateHome string
	LocalAppData string
}

var (
	errNoHome         = errors.New("telemetry: no absolute home directory")
	errNoLocalAppData = errors.New("telemetry: no absolute LocalAppData")
	errUnsupportedOS  = errors.New("telemetry: unsupported platform")
)

// ResolveRoot returns the telemetry root. It touches no file: the caller
// checks and creates the directory.
func ResolveRoot(l Location) (string, error) {
	base, err := duologDir(l)
	if err != nil {
		return "", err
	}
	if l.GOOS == "darwin" {
		// .noindex keeps Spotlight out.
		return filepath.Join(base, "telemetry.noindex"), nil
	}
	return filepath.Join(base, "telemetry"), nil
}

// BalanceRoot returns balance's state directory, beside the telemetry root
// and outside its pruning and purge.
func BalanceRoot(l Location) (string, error) {
	base, err := duologDir(l)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "balance"), nil
}

// duologDir is the per-user duolog data directory. WSL reports linux and
// gets the Linux directory, so its store is separate from the Windows one.
func duologDir(l Location) (string, error) {
	switch l.GOOS {
	case "darwin":
		if !absolute(l.GOOS, l.Home) {
			return "", errNoHome
		}
		return filepath.Join(l.Home, "Library", "Application Support", "duolog"), nil
	case "linux":
		// The XDG spec says a relative path is invalid and must be ignored.
		if absolute(l.GOOS, l.XDGStateHome) {
			return filepath.Join(l.XDGStateHome, "duolog"), nil
		}
		if !absolute(l.GOOS, l.Home) {
			return "", errNoHome
		}
		return filepath.Join(l.Home, ".local", "state", "duolog"), nil
	case "windows":
		// Never Roaming (os.UserConfigDir): domain profiles copy it to a
		// server.
		if !absolute(l.GOOS, l.LocalAppData) {
			return "", errNoLocalAppData
		}
		return filepath.Join(l.LocalAppData, "duolog"), nil
	}
	return "", errUnsupportedOS
}

// absolute judges p by the rules of goos rather than the host's, so every
// platform's inputs can be checked on any host.
func absolute(goos, p string) bool {
	if goos != "windows" {
		return strings.HasPrefix(p, "/")
	}
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return true
	}
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return false
	}
	drive := p[0] | 0x20 // lower case
	return 'a' <= drive && drive <= 'z'
}
