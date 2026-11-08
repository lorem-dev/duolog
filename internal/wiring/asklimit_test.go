package wiring

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/install"
	hookfile "github.com/lorem-dev/duolog/internal/install"
)

// The gate must give up before the hook that install writes is killed: an
// agent can let the tool run when it kills a hook.
func TestLongestWaitEndsBeforeTheInstalledHook(t *testing.T) {
	if gate.MaxPersonAskWait+gate.HookMargin > hookfile.GateTimeout*time.Second {
		t.Fatalf("wait %s and margin %s do not fit in a hook of %d seconds", gate.MaxPersonAskWait, gate.HookMargin, hookfile.GateTimeout)
	}
	if install.GateTimeout != hookfile.GateTimeout {
		t.Fatalf("install writes %d seconds, the checks expect %d", install.GateTimeout, hookfile.GateTimeout)
	}
}

func putHooks(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeGate(extra string) string {
	return `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude","--event","PreToolUse"]` + extra + `}]}]}}`
}

func codexGate(extra string) string {
	return `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"if command -v duolog >/dev/null 2>&1; then exec duolog gate --agent codex --event PreToolUse; fi"` + extra + `}]}]}}`
}

func TestHookAskLimit(t *testing.T) {
	type files map[string]string
	for name, tc := range map[string]struct {
		agent string
		files files
		want  time.Duration
	}{
		"no hook file keeps the old budget": {"claude", files{}, gate.LegacyHookBudget},
		"an unknown agent keeps it too":     {"other", files{".claude/settings.json": claudeGate(`,"timeout":630`)}, gate.LegacyHookBudget},
		"claude project hook":               {"claude", files{".claude/settings.json": claudeGate(`,"timeout":630`)}, 630 * time.Second},
		"an old hook in any layer wins":     {"claude", files{".claude/settings.json": claudeGate(`,"timeout":630`), ".claude/settings.local.json": claudeGate(`,"timeout":150`)}, 150 * time.Second},
		"a hook with no timeout":            {"claude", files{".claude/settings.json": claudeGate("")}, 600 * time.Second},
		"codex project hook":                {"codex", files{".codex/hooks.json": codexGate(`,"timeout":630`)}, 630 * time.Second},
		"codex old hook":                    {"codex", files{".codex/hooks.json": codexGate(`,"timeout":150`)}, 150 * time.Second},
		"the other agent's hook is ignored": {"codex", files{".claude/settings.json": claudeGate(`,"timeout":630`)}, gate.LegacyHookBudget},
	} {
		t.Run(name, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			for rel, body := range tc.files {
				putHooks(t, filepath.Join(root, filepath.FromSlash(rel)), body)
			}
			environ := []string{"HOME=" + home, "USERPROFILE=" + home}
			if got := HookAskLimit(tc.agent, "PreToolUse", root, root, environ); got != tc.want {
				t.Fatalf("limit %s, want %s", got, tc.want)
			}
		})
	}
}

// A worker in a linked worktree loads the hooks of its own folder too.
func TestHookAskLimitReadsTheWorkFolder(t *testing.T) {
	home, root, work := t.TempDir(), t.TempDir(), t.TempDir()
	putHooks(t, filepath.Join(root, ".claude", "settings.json"), claudeGate(`,"timeout":630`))
	putHooks(t, filepath.Join(work, ".claude", "settings.json"), claudeGate(`,"timeout":150`))
	environ := []string{"HOME=" + home, "USERPROFILE=" + home}
	if got := HookAskLimit("claude", "PreToolUse", root, work, environ); got != 150*time.Second {
		t.Fatalf("limit %s, want 150s", got)
	}
}

func TestHookAskLimitReadsTheUserHooks(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	putHooks(t, filepath.Join(home, ".codex", "hooks.json"), codexGate(`,"timeout":630`))
	environ := []string{"HOME=" + home, "USERPROFILE=" + home}
	if got := HookAskLimit("codex", "PreToolUse", root, root, environ); got != 630*time.Second {
		t.Fatalf("limit %s, want 630s", got)
	}
}
