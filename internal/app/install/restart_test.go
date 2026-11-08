package install

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/plugin"
)

// A run that writes the skills or the server entry ends with the restart
// line; a run that finds both current prints none.
func TestRunSaysToRestartOnlyWhenSomethingChanged(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Home: home, Root: root}
	if r := run(t, o); !slices.Contains(r.Notes, RestartNote) || r.Notes[len(r.Notes)-1] != RestartNote {
		t.Fatalf("first install notes %q", r.Notes)
	}
	if r := run(t, o); slices.Contains(r.Notes, RestartNote) {
		t.Fatalf("a current install says %q", r.Notes)
	}
	o.Scope = "user"
	if r := run(t, o); slices.Contains(r.Notes, RestartNote) {
		t.Fatalf("a current user install says %q", r.Notes)
	}
}

// ageInstall sets every user skill file and the stamp of the server entry to
// the time old.
func ageInstall(t *testing.T, d Deps, home string, old time.Time) {
	t.Helper()
	for _, a := range d.Agents {
		for _, name := range plugin.Skills {
			if p := userSkill(home, a.Name, name); isThere(p, "") {
				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := os.Chtimes(serverStamp(home), old, old); err != nil {
		t.Fatal(err)
	}
}

// NewerThan compares the user skill files and the stamp with a time.
func TestNewerThanComparesSkillFiles(t *testing.T) {
	home, root := setup(t)
	run(t, Options{Scope: "project", Home: home, Root: root})
	now := time.Now()
	d := testDeps()
	if !d.NewerThan(home, now.Add(-time.Hour)) {
		t.Fatal("a skill written after the start is not newer")
	}
	ageInstall(t, d, home, now.Add(-2*time.Hour))
	if d.NewerThan(home, now.Add(-time.Hour)) || d.NewerThan("", now.Add(-time.Hour)) {
		t.Fatal("old skills or no home count as newer")
	}
}

// serverFiles lists the agents' state files that hold the server entry.
func serverFiles(t *testing.T, d Deps, home string) []string {
	t.Helper()
	var files []string
	for _, a := range d.Agents {
		if servers, ok := a.Installer.(MCPServers); ok {
			if path, ok := mcpServerFile(servers, home); ok {
				files = append(files, path)
			}
		}
	}
	if len(files) == 0 {
		t.Fatal("no agent has a server file")
	}
	return files
}

// An install that writes only the server entry reaches a lead too, by its own
// stamp: the skills are as old as they were.
func TestNewerThanSeesAnInstallThatWritesOnlyTheServerEntry(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Home: home, Root: root}
	run(t, o)
	d := testDeps()
	now := time.Now()
	since := now.Add(-time.Hour)
	ageInstall(t, d, home, now.Add(-2*time.Hour))
	for _, path := range serverFiles(t, d, home) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if r := run(t, o); !slices.Contains(r.Notes, RestartNote) {
		t.Fatalf("install notes %q", r.Notes)
	}
	if !d.NewerThan(home, since) {
		t.Fatal("an install of the server entry alone is not newer")
	}
}

// The agent rewrites its state file all the time, so that file's time says
// nothing, and a run that changes nothing moves no stamp.
func TestNewerThanIgnoresTheServerFileItself(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Home: home, Root: root}
	run(t, o)
	d := testDeps()
	now := time.Now()
	since := now.Add(-time.Hour)
	ageInstall(t, d, home, now.Add(-2*time.Hour))
	for _, path := range serverFiles(t, d, home) {
		if isThere(path, "") {
			if err := os.Chtimes(path, now, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d.NewerThan(home, since) {
		t.Fatal("a touched server file counts as an install")
	}
	run(t, o)
	if d.NewerThan(home, since) {
		t.Fatal("a current install moved the stamp")
	}
}
