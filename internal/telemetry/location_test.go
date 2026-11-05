package telemetry

import (
	"path/filepath"
	"testing"
)

// noOverride points every environment variable a root could be read from at
// somewhere else; the roots come only from the Location.
func noOverride(t *testing.T) {
	t.Helper()
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "LOCALAPPDATA", "APPDATA", "DUOLOG_TELEMETRY", "DUOLOG_TELEMETRY_ROOT"} {
		t.Setenv(name, elsewhere)
	}
}

func TestResolveRoot(t *testing.T) {
	noOverride(t)
	const (
		home     = "/home/tester"
		macHome  = "/Users/tester"
		winHome  = `C:\Users\tester`
		local    = `C:\Users\tester\AppData\Local`
		wslLocal = "/mnt/c/Users/tester/AppData/Local"
	)
	cases := []struct {
		name string
		l    Location
		want string
	}{
		{"macos", Location{GOOS: "darwin", Home: macHome, XDGStateHome: "/xdg/state"},
			filepath.Join(macHome, "Library", "Application Support", "duolog", "telemetry.noindex")},
		{"linux xdg", Location{GOOS: "linux", Home: home, XDGStateHome: "/xdg/state"},
			filepath.Join("/xdg/state", "duolog", "telemetry")},
		{"linux xdg without home", Location{GOOS: "linux", XDGStateHome: "/xdg/state"},
			filepath.Join("/xdg/state", "duolog", "telemetry")},
		{"linux relative xdg", Location{GOOS: "linux", Home: home, XDGStateHome: "xdg/state"},
			filepath.Join(home, ".local", "state", "duolog", "telemetry")},
		{"linux dot xdg", Location{GOOS: "linux", Home: home, XDGStateHome: "./state"},
			filepath.Join(home, ".local", "state", "duolog", "telemetry")},
		{"linux fallback", Location{GOOS: "linux", Home: home},
			filepath.Join(home, ".local", "state", "duolog", "telemetry")},
		{"windows local", Location{GOOS: "windows", Home: winHome, LocalAppData: local},
			filepath.Join(local, "duolog", "telemetry")},
		{"wsl", Location{GOOS: "linux", Home: home, LocalAppData: wslLocal},
			filepath.Join(home, ".local", "state", "duolog", "telemetry")},
	}
	for _, c := range cases {
		got, err := ResolveRoot(c.l)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// Windows data never goes to Roaming, and never falls back to home.
	got, _ := ResolveRoot(Location{GOOS: "windows", Home: winHome, LocalAppData: local})
	if roaming := filepath.Join(winHome, "AppData", "Roaming", "duolog", "telemetry"); got == roaming {
		t.Errorf("windows root is in Roaming: %q", got)
	}
	// WSL and Windows use distinct stores.
	wsl, _ := ResolveRoot(Location{GOOS: "linux", Home: home, LocalAppData: wslLocal})
	if win, _ := ResolveRoot(Location{GOOS: "windows", Home: winHome, LocalAppData: local}); wsl == win {
		t.Errorf("wsl and windows share %q", wsl)
	}

	missing := []struct {
		name string
		l    Location
	}{
		{"macos without home", Location{GOOS: "darwin", XDGStateHome: "/xdg/state", LocalAppData: local}},
		{"macos relative home", Location{GOOS: "darwin", Home: "Users/tester"}},
		{"linux without home", Location{GOOS: "linux"}},
		{"linux relative xdg without home", Location{GOOS: "linux", XDGStateHome: "xdg/state"}},
		{"linux relative home", Location{GOOS: "linux", Home: "home/tester"}},
		{"windows without local data", Location{GOOS: "windows", Home: winHome}},
		{"windows relative local data", Location{GOOS: "windows", Home: winHome, LocalAppData: `AppData\Local`}},
		{"wsl without home", Location{GOOS: "linux", LocalAppData: wslLocal}},
		{"unsupported", Location{GOOS: "plan9", Home: home}},
		{"empty", Location{}},
	}
	for _, c := range missing {
		if got, err := ResolveRoot(c.l); err == nil || got != "" {
			t.Errorf("%s: got %q, %v; want an error", c.name, got, err)
		}
		if got, err := BalanceRoot(c.l); err == nil || got != "" {
			t.Errorf("balance %s: got %q, %v; want an error", c.name, got, err)
		}
	}
}

func TestBalanceSibling(t *testing.T) {
	noOverride(t)
	cases := []struct {
		name string
		l    Location
		base string
	}{
		{"macos", Location{GOOS: "darwin", Home: "/Users/tester"},
			filepath.Join("/Users/tester", "Library", "Application Support", "duolog")},
		{"linux xdg", Location{GOOS: "linux", Home: "/home/tester", XDGStateHome: "/xdg/state"},
			filepath.Join("/xdg/state", "duolog")},
		{"linux fallback", Location{GOOS: "linux", Home: "/home/tester", XDGStateHome: "xdg/state"},
			filepath.Join("/home/tester", ".local", "state", "duolog")},
		{"windows", Location{GOOS: "windows", Home: `C:\Users\tester`, LocalAppData: `C:\Users\tester\AppData\Local`},
			filepath.Join(`C:\Users\tester\AppData\Local`, "duolog")},
	}
	for _, c := range cases {
		balance, err := BalanceRoot(c.l)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if want := filepath.Join(c.base, "balance"); balance != want {
			t.Errorf("%s: got %q, want %q", c.name, balance, want)
		}
		root, err := ResolveRoot(c.l)
		if err != nil {
			t.Errorf("%s root: %v", c.name, err)
			continue
		}
		// Balance sits beside telemetry, never inside it.
		if filepath.Dir(balance) != filepath.Dir(root) || balance == root {
			t.Errorf("%s: balance %q is not a sibling of %q", c.name, balance, root)
		}
	}
}
