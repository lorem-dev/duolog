package main

import "testing"

// TestHelpNamesAgents pins the --agent help of install, sync and lead register
// while two agents are registered; a third agent changes it by name only.
func TestHelpNamesAgents(t *testing.T) {
	d := testDeps(t)
	tests := []struct {
		name  string
		path  []string
		flag  string
		usage string
	}{
		{"install", []string{"install"}, "agent", "Which agent: claude, codex or both"},
		{"uninstall", []string{"uninstall"}, "agent", "Which agent: claude, codex or both"},
		{"sync", []string{"sync"}, "from", "Side that holds the sources, claude or codex; needed on the first run"},
		{"lead register", []string{"lead", "register"}, "agent", "The agent of this session, claude or codex, when both are set in the environment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := newRootCommand(d).Find(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			f := cmd.Flags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("no --%s flag", tt.flag)
			}
			if f.Usage != tt.usage {
				t.Errorf("--%s help = %q, want %q", tt.flag, f.Usage, tt.usage)
			}
		})
	}
}
