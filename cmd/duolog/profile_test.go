package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/profile"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestProfileRejectsBadEntry(t *testing.T) {
	learningRepo(t, true)
	_, err := executeBusCommand(t, "profile", "d1", "--entry", "sol/high")
	if err == nil || !strings.Contains(err.Error(), "agent/model/effort") {
		t.Fatal(err)
	}
}

func TestProfilePrintsVerdictScreen(t *testing.T) {
	root := learningRepo(t, true)
	claudeGateRuns(t) // the judge is a read-only Claude worker
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("learning:\n  enabled: true\n  judges: [{agent: claude, model: opus, effort: high}]\n  classes:\n    docs:\n      rubric: [quality]\n      checks: [git --version, no-such-program-x]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := journal.Head(root)
	if err != nil {
		t.Fatal(err)
	}
	r := journal.Delegation{ID: "d-profile", Task: "t-profile", Step: 1, Class: "docs", Ladder: "docs", Role: "docs", Entry: "claude/opus/high", Agent: "claude", Model: "opus", Effort: "high", Base: base, Done: true}
	if err := j.Append("delegations.ndjson", r); err != nil {
		t.Fatal(err)
	}
	dir := j.TaskDir(r.Task, r.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"task.md": "## Goal\nWrite a note.\n## Read\n## Write\n- note.txt\n## Limits\nStay focused.\n## Check\nInspect.\n", "before.patch": "", "result.patch": "", "answer.json": `{"summary":"original result"}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output := func(summary string) string {
		data, err := json.Marshal(map[string]any{"structured_output": map[string]any{"done": true, "summary": summary, "files": []string{}, "needs": []string{}}, "usage": map[string]int{"input_tokens": 100, "output_tokens": 50}})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	fake := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"auth status": {Stdout: `{"loggedIn":true}`},
		"-p":          {Sequence: []testbin.Scenario{{Stdout: output("replay result"), Writes: map[string]string{"note.txt": "replay\n"}}, {Stdout: output(`{"scores":[{"criterion":"quality","a":3,"b":2,"reason":"one is clearer"}]}`)}}},
	}})
	out, err := executeBusCommand(t, "profile", r.ID, "--entry", "claude/sonnet/medium", "--lead", "task-lead")
	if err != nil {
		t.Fatal(out, err)
	}
	previous := -1
	for _, want := range []string{"verdict ", "CRITERION", "checks:", "cost:", "confirm"} {
		i := strings.Index(out, want)
		if i <= previous {
			t.Fatalf("missing or out of order %q: %s", want, out)
		}
		previous = i
	}
	for _, want := range []string{"docs docs, original claude/opus/high vs profile claude/sonnet/medium; judge claude/opus/high, blind, order shuffled", "original 1/2 passed, profile 1/2 passed", "quality", "one is clearer", "duolog verdict ", " correct "} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	vs := readVerdicts(t, j)
	if len(vs) != 1 || vs[0].Kind != "verdict" {
		t.Fatal(vs)
	}
	ds, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
	if err != nil || len(ds) != 2 || ds[1].ProfileOf != r.ID {
		t.Fatal(ds, err)
	}
	args, err := os.ReadFile(fake + ".args.json")
	if err != nil || !strings.Contains(string(args), "opus") {
		t.Fatal(string(args), err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", profile.WorktreeDir)); !os.IsNotExist(err) {
		t.Fatal("profile worktree remains", err)
	}
}

func TestProfileRefusesWorker(t *testing.T) {
	root := learningRepo(t, true)
	t.Setenv("DUOLOG_DEPTH", "1")
	_, err := executeBusCommand(t, "profile", "d1", "--entry", "codex/sol/high")
	if err == nil || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", "journal")); !os.IsNotExist(err) {
		t.Fatal("worker created journal", err)
	}
}
