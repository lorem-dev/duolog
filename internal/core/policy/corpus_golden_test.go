package policy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// checkCorpusGolden compares got with testdata/corpus/<name>, or rewrites the
// file under -update.
func checkCorpusGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "corpus", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden\n got: %s\nwant: %s", name, got, want)
	}
}

// TestEvaluateCorpusGolden pins Evaluate over a fixed corpus of actions in
// three modes, so that moving the gate's decisions cannot change one. The
// golden keeps the mode, the action, the decision kind and the rule, not the
// reason, which may name a temp folder.
func TestEvaluateCorpusGolden(t *testing.T) {
	cfg := policy(t)
	work, protected := t.TempDir(), t.TempDir()
	denies := []config.Rule{{Action: "read", Match: ".env", Decision: Deny}}
	base := func(kind, target string) Action {
		return Action{Kind: kind, Target: target, Role: "review", Work: work, Cwd: work, ProtectedRoots: []string{protected}}
	}
	with := func(a Action, f func(*Action)) Action { f(&a); return a }
	corpus := []Action{
		base("exec", "git status"),
		base("exec", "rm -rf /"),
		base("exec", "cat .env"),
		base("exec", "cat ~/.ssh/id_rsa"),
		base("exec", "uv run git push"),
		base("exec", "codex exec hi"),
		base("exec", "npx @openai/codex"),
		base("exec", "ls > .git/config"),
		with(base("exec", "Get-Content .env"), func(a *Action) { a.Shell = "powershell" }),
		base("read", ".duolog/journal/x"),
		base("read", ".duolog/config.yaml"),
		with(base("read", `C:\work\.env`), func(a *Action) { a.Shell = "windows" }),
		with(base("read", "a.go:stream"), func(a *Action) { a.Shell = "windows" }),
		base("write", "src/a.go"),
		with(base("write", ".duolog/tasks/p1/report.md"), func(a *Action) { a.Peer = "p1" }),
		base("write", filepath.ToSlash(filepath.Join(protected, "x"))),
		base("mcp", "srv/tool"),
		base("spawn", "Task"),
		base("net", "https://example.invalid"),
		base("unknown", "Frobnicate"),
	}
	var got bytes.Buffer
	for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
		for _, a := range corpus {
			d := Evaluate(cfg, LeadRules{Deny: denies}, m, a)
			target := strings.ReplaceAll(a.Target, filepath.ToSlash(protected), "<protected>")
			line, err := json.Marshal(struct {
				Mode   string `json:"mode"`
				Action string `json:"action"`
				Kind   string `json:"kind"`
				Rule   string `json:"rule"`
			}{string(m), a.Kind + " " + target, d.Kind, d.Rule})
			if err != nil {
				t.Fatal(err)
			}
			got.Write(line)
			got.WriteByte('\n')
		}
	}
	checkCorpusGolden(t, "evaluate.json", got.Bytes())
}
