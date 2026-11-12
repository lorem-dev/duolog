package wiring

import (
	"os"
	"path/filepath"
	"testing"
)

// leadWindowConfig gives the test a home and config folders of its own,
// writes the user config text there, and stubs the plan read: plan, and
// reads counts the reads.
func leadWindowConfig(t *testing.T, text, plan string) (root string, reads *int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("APPDATA", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	root = t.TempDir()
	if text != "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "duolog"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "duolog", "config.yaml"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	saved := leadPlan
	t.Cleanup(func() { leadPlan = saved })
	leadPlan = func() (string, string, bool) { n++; return plan, "test source", plan != "" }
	return root, &n
}

func TestLeadWindowFollowsThePlanRead(t *testing.T) {
	for _, c := range []struct {
		plan string
		want int
	}{{"max", 500000}, {"pro", 300000}, {"ultra", 700000}, {"enterprise", 300000}, {"", 300000}} {
		root, reads := leadWindowConfig(t, "", c.plan)
		w, err := leadWindow(root)
		if err != nil {
			t.Fatal(err)
		}
		if w.Window != c.want || w.Plan != c.plan || w.Source != "test source" || w.Fallback != (c.plan == "") || *reads != 1 {
			t.Fatalf("plan %q: got %+v after %d reads, want %d", c.plan, w, *reads, c.want)
		}
	}
}

func TestLeadWindowOfAFixedWindowReadsNoPlan(t *testing.T) {
	for text, want := range map[string]int{
		"workers:\n  compact_window: 300000\n": 300000,
		"workers:\n  compact_window: 0\n":      0,
	} {
		root, reads := leadWindowConfig(t, text, "max")
		w, err := leadWindow(root)
		if err != nil {
			t.Fatal(err)
		}
		if w.Window != want || *reads != 0 {
			t.Fatalf("%q: got %+v after %d reads", text, w, *reads)
		}
	}
}
