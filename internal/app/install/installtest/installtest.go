// Package installtest gives tests of app/install and of the agent adapters a
// fake Installer and the contract that every Installer passes. Only tests
// import it.
package installtest

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/install"
)

// Port is the Installer of app/install under a name that an adapter's test
// can use without importing app/install, which an agent adapter may not.
type Port = launchtest.Installer

// ourHooks is the document the fake merges: one gate hook.
const ourHooks = `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog","args":["gate"],"timeout":150}]}]}}`

// Call is one call of the fake.
type Call struct {
	Method string
	Args   []string
}

// Installer is an Installer that records each call. It merges a fixed
// document of one gate hook with the real merge, puts its files under the
// folders it is given, and reads AGENTS.md as its instruction file.
type Installer struct {
	mu    sync.Mutex
	calls []Call
}

// Calls are the calls so far, in order.
func (f *Installer) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

func (f *Installer) record(method string, args ...string) {
	f.mu.Lock()
	f.calls = append(f.calls, Call{method, args})
	f.mu.Unlock()
}

// HookFile is .fake/hooks.json under base.
func (f *Installer) HookFile(scope, base, home string) string {
	f.record("HookFile", scope, base, home)
	return filepath.Join(base, ".fake", "hooks.json")
}

// SkillDir is .fake/skills under base.
func (f *Installer) SkillDir(scope, base, home string) string {
	f.record("SkillDir", scope, base, home)
	return filepath.Join(base, ".fake", "skills")
}

// Hooks merges the fixed document into existing; a timeout of zero removes it.
func (f *Installer) Hooks(existing []byte, timeout int) ([]byte, []string, error) {
	f.record("Hooks")
	var ours []byte
	if timeout != 0 {
		ours = []byte(ourHooks)
	}
	data, err := install.Merge(existing, ours)
	return data, nil, err
}

// InstructionFiles is AGENTS.md under root.
func (f *Installer) InstructionFiles(root string) (string, []string) {
	f.record("InstructionFiles", root)
	path := filepath.Join(root, "AGENTS.md")
	return path, []string{path}
}

// InstallerContract runs the cases that every Installer must pass, the fake
// and each agent's adapter: duolog's hooks merged into a file leave a second
// merge unchanged, keep a foreign handler, and go again when the timeout is
// zero without touching the foreign one; the hook file of the project scope
// is under the project. What only one agent has, such as the golden document
// or Codex's approvals, is that adapter's own test.
func InstallerContract(t *testing.T, newInstaller func(t *testing.T) Port) {
	t.Helper()
	const foreign = `{"model":"x","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other"}]}]}}`
	t.Run("second merge changes nothing", func(t *testing.T) {
		i := newInstaller(t)
		first, _, err := i.Hooks(nil, install.GateTimeout)
		if err != nil || len(first) == 0 {
			t.Fatalf("Hooks into no file: %q, %v", first, err)
		}
		second, _, err := i.Hooks(first, install.GateTimeout)
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("a second merge changed the file (%v)\nfirst:  %s\nsecond: %s", err, first, second)
		}
	})
	t.Run("foreign handler is kept", func(t *testing.T) {
		i := newInstaller(t)
		merged, _, err := i.Hooks([]byte(foreign), install.GateTimeout)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(merged, []byte(`"command": "other"`)) || !bytes.Contains(merged, []byte(`"model": "x"`)) {
			t.Fatalf("foreign content lost:\n%s", merged)
		}
		removed, _, err := i.Hooks(merged, 0)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(removed, []byte("duolog")) || !bytes.Contains(removed, []byte(`"command": "other"`)) {
			t.Fatalf("removing duolog's hooks must leave only the foreign one:\n%s", removed)
		}
	})
	t.Run("project hook file is under the project", func(t *testing.T) {
		i := newInstaller(t)
		base, home := t.TempDir(), t.TempDir()
		underProject(t, "HookFile(project)", base, i.HookFile("project", base, home))
		underProject(t, "SkillDir(project)", base, i.SkillDir("project", base, home))
	})
}

// underProject fails the test unless path is below base.
func underProject(t *testing.T, what, base, path string) {
	t.Helper()
	if rel, err := filepath.Rel(base, path); err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("%s = %q is not under %q (%v)", what, path, base, err)
	}
}
