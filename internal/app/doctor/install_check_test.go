package doctor

import (
	"context"
	"os"
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

func TestDoctorWithoutInstallCheck(t *testing.T) {
	cfg, opts, _ := setup(t)
	saved := testDeps.InstallCheck
	t.Cleanup(func() { testDeps.InstallCheck = saved })
	testDeps.InstallCheck = nil
	contains(t, findings(t, cfg, opts), "MCP bridge")
}
