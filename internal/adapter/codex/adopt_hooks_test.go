package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/pelletier/go-toml/v2"
)

// currentDoc is the hooks document duolog install writes for Codex now.
func currentDoc(t *testing.T) ([]byte, map[string][]install.Group) {
	t.Helper()
	current, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]install.Group `json:"hooks"`
	}
	if err := json.Unmarshal(current, &doc); err != nil {
		t.Fatal(err)
	}
	return current, doc.Hooks
}

// The branch commits hooks of an older install: other gate timeouts, and an
// older POSIX guard without commandWindows for the other hooks. Install
// rewrites only the trusted main checkout, so the next linked worktree still
// loads the committed forms, and gets the approvals with their own hashes.
func TestAdoptWorktreeApprovesOlderCommittedHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the older form here has no commandWindows, which Windows needs")
	}
	f := newAdoptFixture(t)
	current, doc := currentDoc(t)
	gates := map[string]bool{}
	for event, groups := range doc {
		h := &groups[0].Hooks[0]
		if strings.Contains(h.Command, "duolog gate ") {
			gates[event] = true
			h.Timeout = 150
			continue
		}
		h.Command, h.CommandWindows = "if command -v duolog; then exec duolog hook "+event+" --agent codex; fi", ""
	}
	older, err := json.Marshal(map[string]any{"hooks": doc})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.lead, ".codex", "hooks.json"), string(older))
	git(t, f.lead, "add", ".codex/hooks.json")
	git(t, f.lead, "commit", "-q", "-m", "older hooks")
	wt := filepath.Join(f.lead, ".worktrees", "older")
	git(t, f.lead, "worktree", "add", "-q", "--detach", wt)
	writeFile(t, filepath.Join(f.lead, ".codex", "hooks.json"), string(current))
	lines, err := (Trust{}).AdoptWorktree(f.lead, wt, os.Environ())
	if err != nil || len(lines) != 1+len(doc) {
		t.Fatalf("lines=%q err=%v", lines, err)
	}
	var cfg adoptConfig
	if err := toml.Unmarshal(f.read(t), &cfg); err != nil {
		t.Fatal(err)
	}
	for event := range doc {
		key := filepath.Join(wt, ".codex", "hooks.json") + ":" + snakeEvent(event) + ":0:0"
		want, err := hookHash(older, event, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Hooks.State[key]["trusted_hash"] != want {
			t.Errorf("%s (gate %v): approval=%v want %s", event, gates[event], cfg.Hooks.State[key], want)
		}
	}
	if data, err := os.ReadFile(filepath.Join(wt, ".codex", "hooks.json")); err != nil || !bytes.Equal(data, older) {
		t.Fatal("target hooks changed", err)
	}
	if s, err := hooks(wt); err != nil || !s.Trusted || s.Unproven != "" {
		t.Fatalf("hooks=%+v err=%v", s, err)
	}
	if got := (Trust{}).WorktreeTrustProblem(f.lead, wt, os.Environ()); strings.Contains(got, "not the same") {
		t.Fatal(got)
	}
}

// A foreign handler before duolog's gate moves the gate's place; the
// approval follows the identical gate, never the foreign handler.
func TestAdoptWorktreeLeavesForeignHandlersUnapproved(t *testing.T) {
	f := newAdoptFixture(t)
	_, hooks := currentDoc(t)
	gate := hooks["PreToolUse"][0].Hooks[0]
	foreign := install.Handler{Type: "command", Command: "/tmp/duolog gate --agent codex --event PreToolUse"}
	hooks["PreToolUse"][0].Hooks = []install.Handler{foreign, gate}
	target, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.worktree, ".codex", "hooks.json"), string(target))
	if _, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil {
		t.Fatal(err)
	}
	var cfg adoptConfig
	if err := toml.Unmarshal(f.read(t), &cfg); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.worktree, ".codex", "hooks.json")
	if _, ok := cfg.Hooks.State[path+":pre_tool_use:0:0"]; ok {
		t.Fatal("path command approved")
	}
	if _, ok := cfg.Hooks.State[path+":pre_tool_use:0:1"]; !ok {
		t.Fatal("shifted duolog hook not approved")
	}
}

// A path program, or the exec form with args that Codex does not run, is
// never duolog's own: Codex would run something else, or bare duolog, which
// prints help and gates nothing.
func TestAdoptWorktreeRefusesPathAndArgsPrograms(t *testing.T) {
	for _, h := range []map[string]any{
		{"type": "command", "command": "./x/duolog", "args": []string{"gate"}},
		{"type": "command", "command": "/tmp/duolog", "args": []string{"gate"}},
		{"type": "command", "command": `C:\tools\duolog.exe`, "args": []string{"gate"}},
		{"type": "command", "command": "duolog", "args": []string{"gate", "--agent", "codex", "--event", "PreToolUse"}},
	} {
		t.Run(h["command"].(string), func(t *testing.T) {
			f := newAdoptFixture(t)
			doc, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"hooks": []any{h}}}}})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(f.worktree, ".codex", "hooks.json"), string(doc))
			before := f.read(t)
			lines, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ())
			if err != nil || len(lines) != 0 || !bytes.Equal(before, f.read(t)) {
				t.Fatalf("lines=%q err=%v", lines, err)
			}
		})
	}
}

func TestOwnCodexHandler(t *testing.T) {
	gateArgs := "gate --agent codex --event PreToolUse"
	posixGate, windowsGate := command("linux", gateArgs, true), command("windows", gateArgs, true)
	posixHook := command("linux", "hook Stop --agent codex", false)
	for _, c := range []struct {
		name, event, posix, windows string
		args                        []string
		want                        bool
	}{
		{"current gate", "PreToolUse", posixGate, windowsGate, nil, true},
		{"extra shell", "PreToolUse", posixGate + "; other", windowsGate, nil, false},
		{"path program", "PreToolUse", strings.Replace(posixGate, "then exec duolog", "then exec /tmp/duolog", 1), windowsGate, nil, false},
		{"substitution", "Stop", "duolog hook $(other)", "", nil, false},
		{"args form", "PreToolUse", "duolog", "", []string{"gate", "--agent", "codex", "--event", "PreToolUse"}, false},
		{"windows form in the posix slot", "PreToolUse", windowsGate, windowsGate, nil, false},
		{"posix form in the windows slot", "Stop", posixHook, posixHook, nil, false},
		{"older gate guard", "PreToolUse", "if command -v duolog; then exec duolog " + gateArgs + "; fi", "", nil, false},
		{"older hook guard", "Stop", "if command -v duolog; then exec duolog hook Stop --agent codex; fi", "", nil, runtime.GOOS != "windows"},
		{"hook of another event", "Stop", "if command -v duolog; then exec duolog hook SessionEnd --agent codex; fi", "", nil, false},
	} {
		if got := ownCodexHandler(c.event, c.posix, c.windows, c.args, false); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
