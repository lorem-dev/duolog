package doctor

import (
	"strings"
	"testing"
)

// TestPluginFindings warns only for available agents with missing plugins,
// with the note the agent's adapter gives, and keeps the Claude pane line.
func TestPluginFindings(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		for _, available := range []bool{false, true} {
			for _, installed := range []bool{false, true} {
				note := name + ": duolog plugin is not installed; run " + name + " plugin marketplace add lorem-dev/duolog"
				d := Deps{
					Names:     []string{name},
					Available: func(string) bool { return available },
					PluginMissing: func(agent, _, _ string) string {
						if agent != name {
							t.Fatalf("checked agent %s, want %s", agent, name)
						}
						if installed {
							return ""
						}
						return note
					},
					ClaudePlugin: func(string, string) bool { return true },
				}
				findings := installFindings(d, t.TempDir())
				warnings, pane := 0, false
				for _, f := range findings {
					pane = pane || f.Level == "info" && strings.HasPrefix(f.Message, "Claude pane:")
					if f.Level != "warning" {
						continue
					}
					warnings++
					if f.Message != note {
						t.Fatalf("warning %q, want %q", f.Message, note)
					}
				}
				want := 0
				if available && !installed {
					want = 1
				}
				if warnings != want || !pane {
					t.Fatalf("%s available %v installed %v: %v", name, available, installed, findings)
				}
			}
		}
	}
}

// TestPluginFindingIsNotRepeated ensures the shared install checks do not
// repeat doctor's missing-plugin warning.
func TestPluginFindingIsNotRepeated(t *testing.T) {
	d := Deps{
		Names:         []string{"claude"},
		Available:     func(string) bool { return true },
		PluginMissing: func(string, string, string) string { return "claude: duolog plugin is not installed" },
	}
	line := pluginFindings(d, "", "")[0].Message
	d.InstallCheck = func(string, string) []string { return []string{line} }
	if got := installFindings(d, ""); len(got) != 1 {
		t.Fatalf("duplicate findings: %v", got)
	}
}
