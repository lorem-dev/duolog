package delegate

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func TestUntrustedCodexFallsBackToClaude(t *testing.T) {
	for _, missing := range []string{"hooks", "folder", "stale"} {
		t.Run(missing, func(t *testing.T) {
			h := newHarness(t)
			h.trust(false)
			if missing == "folder" {
				h.codexHooksOnly()
			}
			if missing == "stale" {
				h.trust(true)
				key := filepath.Join(h.root, ".codex", "hooks.json") + ":PreToolUse:0:0"
				put(t, filepath.Join(os.Getenv("HOME"), ".codex", "duolog-stale-approvals.json"), "{"+strconv.Quote(key)+":\"fixture-hash\"}")
			}
			role := h.cfg.Roles["implement"]
			role.Use = config.Target{Agent: "codex", Model: "sol", Effort: "medium"}
			role.Fallback = config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}
			h.cfg.Roles["implement"] = role
			var stderr bytes.Buffer
			out, err := h.runWith("implement", Options{Stderr: &stderr})
			if err != nil || !out.Result.Done {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			h.requireCalls(1)
			if h.calls[0].Route.Agent != "claude" {
				t.Fatalf("started %s", h.calls[0].Route.Agent)
			}
			for _, want := range []string{"Codex skipped", h.root, "run codex", "/hooks"} {
				if !strings.Contains(out.Line, want) {
					t.Errorf("line %q lacks %q", out.Line, want)
				}
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr %q lacks %q", stderr.String(), want)
				}
			}
			unavailable, err := h.reg.Unavailable()
			if err != nil || unavailable["codex"] {
				t.Fatalf("persistent unavailable=%v err=%v", unavailable, err)
			}
		})
	}
}
