package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPPlanCannotOverwriteNewState(t *testing.T) {
	// Kills replacing Claude's live state with a stale installation snapshot.
	home, root := setup(t)
	o := Options{Agent: "claude", Scope: "project", Home: home, Root: root}
	d := testDeps()
	agents, base, err := d.resolve(o)
	if err != nil {
		t.Fatal(err)
	}
	edits, _, _, err := planAgents(agents, o, base)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude.json")
	fresh := `{"newState":"keep"}`
	write(t, path, fresh)
	report := Report{}
	err = writeEdits(edits, &report)
	// A stale plan may be refused, but must never replace the new state.
	if got := read(t, path); got != fresh && !strings.Contains(got, `"newState"`) {
		t.Fatalf("state lost: %s (err %v)", got, err)
	}
}

func TestInstallRejectsSharedSkillDestinations(t *testing.T) {
	// Kills planning both agents' different skills into the same physical folder.
	home, root := setup(t)
	claude := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claude, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claude, filepath.Join(home, ".agents", "skills")); err != nil {
		t.Skip(err)
	}
	_, err := testDeps().Run(Options{Agent: "both", Scope: "project", Home: home, Root: root})
	if err == nil {
		t.Fatal("shared skill destinations accepted")
	}
	if entries, err := os.ReadDir(claude); err != nil || len(entries) != 0 {
		t.Fatalf("shared skills modified: %v %v", entries, err)
	}
}

func TestRefreshRejectsSharedSkillDestinations(t *testing.T) {
	// Kills automatic refresh writing agent-specific skills into a shared folder.
	home, root := setup(t)
	claude := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claude, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claude, filepath.Join(home, ".agents", "skills")); err != nil {
		t.Skip(err)
	}
	if _, err := testDeps().RefreshSkills(root, home); err == nil {
		t.Fatal("shared skill refresh accepted")
	}
}
