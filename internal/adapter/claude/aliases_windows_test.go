package claude

import (
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// The temporary folder of a hosted Windows runner has an 8.3 name, such as
// RUNNER~1. Claude's entry keyed by one spelling, with forward slashes as
// Claude writes it, is found from the other spelling, for trust and for the
// MCP decisions.
func TestPaneReadsTheLeadEntryUnderAnotherWindowsSpelling(t *testing.T) {
	r := newStartRig(t)
	long, err := filepath.EvalSymlinks(r.lead)
	if err != nil {
		t.Fatal(err)
	}
	short := testbin.ShortPath(t, r.lead)
	for _, paths := range [][2]string{{long, short}, {short, long}} {
		r.writeProjects(map[string]any{filepath.ToSlash(paths[0]): map[string]any{"hasTrustDialogAccepted": true, "enabledMcpjsonServers": []string{"a"}}})
		if !(Pane{}).TrustsFolder(paths[1], r.environ()) {
			t.Fatalf("%s did not trust %s", paths[0], paths[1])
		}
		s := Pane{}.Settings(agent.PaneStart{LeadRoot: paths[1], Folder: r.folder, Environ: r.environ()})
		if enabled, _ := s["enabledMcpjsonServers"].([]string); len(enabled) != 1 || enabled[0] != "a" {
			t.Fatalf("%s from %s: %v", paths[0], paths[1], s)
		}
	}
}
