package delegate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/run"
)

func TestDelegateRetainsKnownEffortAndBoundsOutput(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Full, "low")
	l, _, _ := h.reg.Lead("lead-123456")
	l.TranscriptPath = filepath.Join(h.root, "empty.jsonl")
	put(t, l.TranscriptPath, "{}\n")
	if err := h.reg.RegisterLead(l); err != nil {
		t.Fatal(err)
	}
	h.results = []run.Result{{Done: true, Summary: strings.Repeat("a", 6000), Files: []string{}, Needs: []string{}}}
	out, err := h.run("implement")
	if err != nil || len(out.Line) > 512 || !strings.Contains(out.Line, "[cut; ") {
		t.Fatal(len(out.Line), out.Line, err)
	}
	if h.calls[0].Route.Effort != "low" {
		t.Fatal("known effort lost", h.calls[0].Route)
	}
}

func TestInlineRespectsLeadReadDeny(t *testing.T) {
	h := newHarness(t)
	put(t, filepath.Join(h.root, ".claude/settings.json"), `{"permissions":{"deny":["Read(private.txt)"]}}`)
	h.lead("claude", mode.Full, "high")
	h.task("- private.txt")
	put(t, filepath.Join(h.root, "private.txt"), "DO_NOT_SEND_PRIVATE_CONTENT")
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	stdin := string(h.calls[0].Stdin)
	if strings.Contains(stdin, "DO_NOT_SEND_PRIVATE_CONTENT") || !strings.Contains(stdin, "Not inlined by read policy") {
		t.Fatal(stdin)
	}
}

// A private usage store inside the work root stays out of the inlined task:
// inlining judges a Read reference with the same protected roots the worker's
// gate hook gets, before any worker hook runs.
func TestInlineKeepsProtectedStoreOut(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Full, "high")
	store := filepath.Join(h.root, "xdg-state", "duolog")
	put(t, filepath.Join(store, "usage.jsonl"), "DO_NOT_SEND_PRIVATE_STORE")
	h.task("- xdg-state/duolog/usage.jsonl")
	if _, err := h.runWith("implement", Options{ProtectedRoots: []string{store}}); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	stdin := string(h.calls[0].Stdin)
	if strings.Contains(stdin, "DO_NOT_SEND_PRIVATE_STORE") || !strings.Contains(stdin, "Not inlined by read policy") {
		t.Fatal(stdin)
	}
}

// A Claude write role without the gate runs with a warning; a read-only role,
// which only the gate keeps read-only, does not start
// (TestGateHeldClaudeWorkerNeedsItsGate).
func TestDelegateWarnsAboutMissingClaudeHooks(t *testing.T) {
	h := newHarness(t)
	h.claudeGate(false)
	out, err := h.runOn("implement", "claude", "sonnet")
	if err != nil || !strings.Contains(out.Line, "Claude has no duolog hooks") {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
}

func TestWorkerWaitMarkerExistsOnlyDuringRun(t *testing.T) {
	h := newHarness(t)
	previous := h.worker()
	h.setWorker(func(ctx context.Context, s run.Spec) (run.Result, error) {
		if waiting, err := h.reg.IsWaiting("lead-123456"); err != nil || !waiting {
			t.Fatal("no waiting marker", err)
		}
		return previous(ctx, s)
	})
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	if waiting, err := h.reg.IsWaiting("lead-123456"); err != nil || waiting {
		t.Fatal("wait marker leaked", err)
	}
}

func TestImportFailureStopsBeforeWorker(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Full, "high")
	put(t, filepath.Join(h.root, ".claude/settings.json"), `{"permissions":{"deny":["UnknownTool(bad)"]}}`)
	out, err := h.run("implement")
	if err == nil || !strings.Contains(out.Line, "lead permissions") {
		t.Fatal(out, err)
	}
	h.requireCalls(0)
}

func TestDefaultLoginVariablesReachWorker(t *testing.T) {
	h := newHarness(t)
	names := []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"}
	keys := []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}
	for _, name := range append(names, keys...) {
		t.Setenv(name, filepath.Join(h.root, "login-"+name))
	}
	// This read-only Claude run needs no Codex trust metadata.
	fake := h.fakeWorker("claude")
	if _, err := h.run("review_final"); err != nil {
		t.Fatal(err)
	}
	env := read(t, fake+".env.json")
	for _, name := range names {
		if !strings.Contains(env, name+"=") {
			t.Fatal("worker lost login variable", name)
		}
	}
	for _, name := range keys {
		if strings.Contains(env, name+"=") {
			t.Fatal("worker inherited billing key", name)
		}
	}
}
