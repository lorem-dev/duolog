package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/calibrate/calibratetest"
)

const gateHooks = `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`

// tempHome points the home and the Codex config at a fresh folder, so no test
// reads or writes the person's own.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	return home
}

// trustCodex writes a Codex config under home that trusts dir and approves
// the gate hook in dir/.codex/hooks.json, as Codex does once the person
// trusts the folder and reviews its hooks.
func trustCodex(t *testing.T, home, dir string, folder, hooks bool) {
	t.Helper()
	var s string
	if folder {
		s += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir)
	}
	if hooks {
		s += fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(dir, ".codex", "hooks.json")+":pre_tool_use:0:0")
	}
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), s)
}

func TestTreeGuardContract(t *testing.T) {
	calibratetest.TreeGuardContract(t, func(t *testing.T) (calibratetest.TreeGuard, func(string), string) {
		home := tempHome(t)
		grant := func(dir string) {
			writeFile(t, filepath.Join(dir, ".codex", "hooks.json"), gateHooks)
			trustCodex(t, home, dir, true, true)
		}
		return Tree{}, grant, "hooks"
	})
}

func TestTreeCheckNeedsFolderAndHooks(t *testing.T) {
	home := tempHome(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".codex", "hooks.json"), gateHooks)
	for _, tc := range []struct {
		name          string
		folder, hooks bool
	}{
		{"nothing trusted", false, false},
		{"folder only", true, false},
		{"hooks only", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trustCodex(t, home, dir, tc.folder, tc.hooks)
			err := (Tree{}).Check(dir, os.Environ())
			if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "hooks") {
				t.Fatalf("Check = %v, want an error naming %s and its hooks", err, dir)
			}
		})
	}
	trustCodex(t, home, dir, true, true)
	if err := (Tree{}).Check(dir, os.Environ()); err != nil {
		t.Fatalf("Check on a trusted tree: %v", err)
	}
}

// The trust is judged from the worker's own environment: the caller's
// CODEX_HOME trusts the tree, but the worker gets no such variable, so Codex
// is refused for the folder trust the worker's own home lacks.
func TestTreeReadsTheWorkersEnvironment(t *testing.T) {
	home := tempHome(t)
	dir := t.TempDir()
	worker := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
	writeFile(t, filepath.Join(dir, ".codex", "hooks.json"), gateHooks)
	// trustCodex writes <home>/.codex/config.toml, so CODEX_HOME is that
	// .codex folder.
	callerHome := t.TempDir()
	codexHome := filepath.Join(callerHome, ".codex")
	trustCodex(t, callerHome, dir, true, true)
	t.Setenv("CODEX_HOME", codexHome)
	trustCodex(t, home, dir, false, true)
	if err := (Tree{}).Check(dir, worker); err == nil {
		t.Error("a tree trusted only in the caller's CODEX_HOME passed")
	}
	// Passed on, the caller's folder is the worker's, and it counts.
	both := append(slices.Clone(worker), "CODEX_HOME="+codexHome)
	if err := (Tree{}).Check(dir, both); err != nil {
		t.Errorf("with CODEX_HOME passed on: %v", err)
	}
}

// Prepare leaves the tree as it is: the hooks are the project's, kept in the
// slot, and duolog never edits the Codex config. It builds no worker
// environment, so one that cannot be built does not fail the tree.
func TestTreePrepareChangesNothing(t *testing.T) {
	tempHome(t)
	dir := t.TempDir()
	broken := func() ([]string, error) { return nil, errors.New("codex environment broken") }
	if err := (Tree{}).Prepare(dir, broken); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("Prepare wrote %v", entries)
	}
}
