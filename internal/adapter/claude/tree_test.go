package claude

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/calibrate/calibratetest"
	"github.com/lorem-dev/duolog/internal/install"
)

// tempHome points the home and the Claude config at a fresh folder, so no
// test reads or writes the person's own.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func gateJSON(t *testing.T) string {
	t.Helper()
	hooks, err := HooksJSON(150)
	if err != nil {
		t.Fatal(err)
	}
	return string(hooks)
}

// env is Prepare's builder of environ.
func env(environ []string) func() ([]string, error) {
	return func() ([]string, error) { return environ, nil }
}

func TestTreeGuardContract(t *testing.T) {
	calibratetest.TreeGuardContract(t, func(t *testing.T) (calibratetest.TreeGuard, func(string), string) {
		tempHome(t)
		return Tree{}, func(string) {}, "gate"
	})
}

// A tree gets the duolog gate for Claude in .claude/settings.local.json when
// nothing else gives it one.
func TestTreePrepareAddsGateUnlessTheUserHasOne(t *testing.T) {
	home := tempHome(t)
	dir := t.TempDir()
	local := filepath.Join(dir, ".claude", "settings.local.json")
	if err := (Tree{}).Prepare(dir, env(os.Environ())); err != nil {
		t.Fatal(err)
	}
	if !install.HasGate(local) {
		t.Fatalf("%s has no duolog gate", local)
	}
	if err := (Tree{}).Check(dir, os.Environ()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	// The person's own Claude settings run the gate: no second copy.
	other := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), gateJSON(t))
	if err := (Tree{}).Prepare(other, env(os.Environ())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.local.json written although the user settings run the gate: %v", err)
	}
}

// The tree's gate is judged from the worker's own environment. The caller's
// CLAUDE_CONFIG_DIR runs the gate, but the worker gets no such variable: the
// tree gets a local gate, and the bare tree is refused until the caller's
// folder is passed on.
func TestTreeReadsTheWorkersEnvironment(t *testing.T) {
	home := tempHome(t)
	worker := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
	caller := t.TempDir()
	writeFile(t, filepath.Join(caller, "settings.json"), gateJSON(t))
	t.Setenv("CLAUDE_CONFIG_DIR", caller)

	bare := t.TempDir()
	if err := (Tree{}).Check(bare, worker); err == nil {
		t.Error("a tree whose gate is only in the caller's CLAUDE_CONFIG_DIR passed")
	}
	dir := t.TempDir()
	if err := (Tree{}).Prepare(dir, env(worker)); err != nil {
		t.Fatal(err)
	}
	if !install.HasGate(filepath.Join(dir, ".claude", "settings.local.json")) {
		t.Error("no local gate in the tree, although the worker's settings have none")
	}
	both := append(slices.Clone(worker), "CLAUDE_CONFIG_DIR="+caller)
	if err := (Tree{}).Check(bare, both); err != nil {
		t.Errorf("with CLAUDE_CONFIG_DIR passed on: %v", err)
	}
}

// disableAllHooks in a layer Claude loads refuses the tree.
func TestTreeCheckRefusesHooksOff(t *testing.T) {
	tempHome(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude", "settings.json"), `{"disableAllHooks":true}`)
	err := (Tree{}).Check(dir, os.Environ())
	if err == nil || !strings.Contains(err.Error(), "disableAllHooks") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("Check with hooks off = %v, want a refusal naming disableAllHooks and %s", err, dir)
	}
}

// A worker with no home folder cannot be shown to run the gate: the tree gets
// its own, and Check says why it cannot read the settings.
func TestTreeWithoutHome(t *testing.T) {
	dir := t.TempDir()
	environ := []string{"PATH=" + os.Getenv("PATH")}
	if err := (Tree{}).Prepare(dir, env(environ)); err != nil {
		t.Fatal(err)
	}
	if !install.HasGate(filepath.Join(dir, ".claude", "settings.local.json")) {
		t.Fatal("no local gate in a tree whose worker names no home")
	}
	if err := (Tree{}).Check(dir, environ); err == nil || !strings.Contains(err.Error(), "env_passthrough") {
		t.Fatalf("Check with no home = %v, want the env_passthrough advice", err)
	}
}
