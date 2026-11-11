package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorWarnsOfStaleInstall(t *testing.T) {
	cfg, opts, _ := setup(t)
	home, _ := os.UserHomeDir()
	line := "claude project install is out of date: .claude/settings.json; run duolog install --scope project --agent claude"
	var asked [][2]string
	saved := testDeps.InstallCheck
	t.Cleanup(func() { testDeps.InstallCheck = saved })
	testDeps.InstallCheck = func(root, home string) []string {
		asked = append(asked, [2]string{root, home})
		return []string{line}
	}
	var found bool
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		if f.Message == line {
			found = f.Level == "warning"
		}
	}
	if !found || len(asked) != 1 || asked[0] != [2]string{opts.Root, home} {
		t.Fatalf("found %t, asked %v", found, asked)
	}
}

func TestDoctorReportsTheLeadCompactWindow(t *testing.T) {
	cfg, opts, _ := setup(t)
	line := "claude lead: compacts at 500000 tokens for plan max, set by duolog in .claude/settings.local.json"
	saved := testDeps.LeadCompact
	t.Cleanup(func() { testDeps.LeadCompact = saved })
	testDeps.LeadCompact = func(root, home string) string { return line }
	var found bool
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		if f.Message == line {
			found = f.Level == "info"
		}
	}
	if !found {
		t.Fatal("no info finding for the lead's compact window")
	}
}

func TestDoctorReportsTheStatusLineAsInfo(t *testing.T) {
	cfg, opts, _ := setup(t)
	line := "claude: status line is yours (~/x); run duolog statusline with the same stdin to add duolog's part"
	saved := testDeps.StatusLineLine
	t.Cleanup(func() { testDeps.StatusLineLine = saved })
	testDeps.StatusLineLine = func(root, home string) string { return line }
	var found bool
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		if f.Message == line {
			found = f.Level == "info"
		}
	}
	if !found {
		t.Fatal("no info finding for the status line")
	}
}

// A Codex lead alerts the person at the end of its turn only with Codex's own
// tui.notifications; doctor says where to set it while it is off.
func TestDoctorTellsOfCodexNotifications(t *testing.T) {
	cfg, opts, a := setup(t)
	home, _ := os.UserHomeDir()
	config := filepath.Join(home, ".codex", "config.toml")
	// The home folder is written as ~, so a model that reads doctor learns no path.
	line := "codex: set tui.notifications = true in ~/.codex/config.toml for an alert when a Codex lead's turn ends"
	var found bool
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		if f.Message == line {
			found = f.Level == "info"
		}
	}
	if !found {
		t.Fatalf("no info line %q", line)
	}
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[tui]\nnotifications = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findings(t, cfg, opts); strings.Contains(got, "tui.notifications") {
		t.Fatalf("notifications on, but doctor says: %s", got)
	}
	// Without Codex there is no Codex lead to alert for.
	_ = os.Remove(config)
	a.codex.Missing = true
	if got := findings(t, cfg, opts); strings.Contains(got, "tui.notifications") {
		t.Fatalf("no codex, but doctor says: %s", got)
	}
}

func TestDoctorWithoutInstallCheck(t *testing.T) {
	cfg, opts, _ := setup(t)
	saved := testDeps.InstallCheck
	t.Cleanup(func() { testDeps.InstallCheck = saved })
	testDeps.InstallCheck = nil
	contains(t, findings(t, cfg, opts), "MCP bridge")
}
