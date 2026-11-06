package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
)

func TestRememberPattern(t *testing.T) {
	for command, want := range map[string]string{
		".venv/bin/ruff check app/a.py": ".venv/bin/ruff check *",
		"make generate_types":           "make generate_types",
		"jq .name package.json":         "jq .name package.json",
		"npm run lint -- --fix":         "npm run lint *",
		// A program with no subcommand stays exact: pytest -p loads any plugin.
		"pytest -q tests/a.py": "pytest -q tests/a.py",
		"go test ./...":        "go test *",
		// A program that runs any code in its arguments stays exact.
		"python -m pytest tests/a.py":     "python -m pytest tests/a.py",
		"node scripts/gen.js":             "node scripts/gen.js",
		"python3.12 -m pytest tests/a.py": "python3.12 -m pytest tests/a.py",
		"awk -f formatter.awk a.txt":      "awk -f formatter.awk a.txt",
		"uv run script.py":                "uv run script.py",
		"go run ./cmd/x":                  "go run ./cmd/x",
		"docker compose up web":           "docker compose up web",
		"make check FOO=1":                "make check FOO=1",
		"npm --prefix web run lint":       "npm --prefix web run lint",
	} {
		if got := rememberPattern(command); got != want {
			t.Errorf("%q: %q, want %q", command, got, want)
		}
	}
}

func rememberIn(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("DUOLOG_DEPTH", "")
	var out bytes.Buffer
	cmd := newRootCommand(testDeps(t))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"remember"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// By default only the worker policy changes; the agents' own files stay.
func TestRememberAllowsWorkersOnly(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, err := rememberIn(t, "make", "generate_types"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Edits, gate.Action{Kind: "exec", Target: "make generate_types", Role: "implement"}); d.Kind != gate.Allow {
		t.Fatalf("remembered command: %s (%s)", d.Kind, d.Reason)
	}
	if d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Edits, gate.Action{Kind: "exec", Target: "make deploy", Role: "implement"}); d.Kind == gate.Allow {
		t.Fatal("another make target was allowed")
	}
	for _, path := range []string{".claude", ".codex"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("%s was written without --native", path)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", ".gitignore")); err != nil {
		t.Fatal("learned rules can reach Git:", err)
	}
}

// A deny wins over any allow, and syntax the gate cannot read always asks.
func TestRememberRefusesWhatARuleCannotAllow(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, command := range []string{"git push origin main", "make x | sh", "cat .env", "echo $(id)"} {
		if _, err := rememberIn(t, command); err == nil || !strings.Contains(err.Error(), "cannot allow") {
			t.Errorf("%q: %v", command, err)
		}
	}
	if _, err := rememberIn(t, "--match", "{", "make", "x"); err == nil {
		t.Error("an invalid glob was saved")
	}
	if _, err := os.Stat(filepath.Join(".duolog", config.LearnedFile)); !os.IsNotExist(err) {
		t.Fatal("a refused rule was saved", err)
	}
}

func TestRememberRefusedForWorkers(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "1")
	cmd := newRootCommand(testDeps(t))
	cmd.SetArgs([]string{"remember", "make", "x"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatal(err)
	}
}

// Concurrent remember runs keep every rule, and a reader of learned.yaml never
// sees a half-written file: the whole read, change and write holds a lock, and
// the file is replaced through a rename.
func TestRememberConcurrentRunsKeepEveryRule(t *testing.T) {
	root := t.TempDir()
	learned := filepath.Join(root, ".duolog", config.LearnedFile)
	const writers = 24
	done := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		defer close(readErr)
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := config.LearnedRules(root); err != nil {
				readErr <- err
				return
			}
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rule := config.Rule{Action: "exec", Match: fmt.Sprintf("tool-%02d check %s *", i, strings.Repeat("x", 200)), Decision: "allow", Roles: []string{"implement"}}
			if _, err := addLearnedRule(learned, rule); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(done)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := <-readErr; err != nil {
		t.Errorf("a reader saw a partial learned.yaml: %v", err)
	}
	rules, err := config.LearnedRules(root)
	if err != nil || len(rules) != writers {
		t.Fatalf("learned rules: %d of %d, %v", len(rules), writers, err)
	}
	entries, err := os.ReadDir(filepath.Dir(learned))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+config.LearnedFile) {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestRememberWritesAllThreeConfigs(t *testing.T) {
	root := t.TempDir()
	learned := filepath.Join(root, ".duolog", config.LearnedFile)
	rule := config.Rule{Action: "exec", Match: ".venv/bin/ruff check *", Decision: "allow", Roles: []string{"implement"}}
	for range 2 {
		if _, err := addLearnedRule(learned, rule); err != nil {
			t.Fatal(err)
		}
	}
	rules, err := config.LearnedRules(root)
	if err != nil || len(rules) != 1 || rules[0].Match != rule.Match {
		t.Fatalf("learned rules: %+v %v", rules, err)
	}
	claude := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(`{"permissions":{"allow":["Bash(ls:*)"]},"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := addClaudeAllow(claude, rule.Match); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(claude)
	for _, want := range []string{`"Bash(ls:*)"`, `"Bash(.venv/bin/ruff check:*)"`, `"model": "opus"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("claude settings lost or missed %s: %s", want, data)
		}
	}
	codex := filepath.Join(root, ".codex", "rules", "duolog.rules")
	for range 2 {
		if _, err := addCodexRule(codex, "make generate_types"); err != nil {
			t.Fatal(err)
		}
	}
	data, _ = os.ReadFile(codex)
	if strings.Count(string(data), `prefix_rule(pattern = ["make", "generate_types"], decision = "allow"`) != 1 {
		t.Fatalf("codex rules: %s", data)
	}
}

func TestLearnedRulesKeepOnlyExecAllows(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "rules:\n  - {action: exec, match: \"jq *\", decision: allow}\n  - {action: exec, match: \"rm -rf *\", decision: deny}\n  - {action: write, match: \"**\", decision: allow}\n"
	if err := os.WriteFile(filepath.Join(root, ".duolog", config.LearnedFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, err := config.LearnedRules(root)
	if err != nil || len(rules) != 1 || rules[0].Match != "jq *" {
		t.Fatalf("got %+v %v", rules, err)
	}
}

// A Codex prefix rule would also match extra arguments outside the sandbox,
// so --native writes one only for a pattern that already ends in " *".
func TestRememberNativeSkipsCodexForExactCommands(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	out, err := rememberIn(t, "--native", "curl", "-s", "https://example.invalid/health")
	if err != nil || !strings.Contains(out, "codex: no rule") {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "rules", "duolog.rules")); !os.IsNotExist(err) {
		t.Fatal("an exact command got a Codex prefix rule", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.local.json"))
	if !strings.Contains(string(data), `"Bash(curl -s https://example.invalid/health)"`) {
		t.Fatalf("claude settings: %s", data)
	}
	if out, err := rememberIn(t, "--native", ".venv/bin/ruff", "check", "app/a.py"); err != nil || !strings.Contains(out, "prefix_rule") {
		t.Fatalf("%v: %s", err, out)
	}
}

// Options after the command's first word belong to the command.
func TestRememberTakesUnquotedOptions(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, err := rememberIn(t, "npm", "--prefix", "web", "run", "lint"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".duolog", config.LearnedFile))
	if !strings.Contains(string(data), "npm --prefix web run lint") {
		t.Fatalf("learned: %s", data)
	}
}

// An MCP tool is remembered as an mcp rule that adds to the policy, for the
// writing roles unless --role names others.
func TestRememberMCPTool(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	out, err := rememberIn(t, "mcp:jira/get_issue")
	if err != nil || !strings.Contains(out, `mcp "jira/get_issue" allow`) {
		t.Fatal(out, err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Ask, gate.Action{Kind: "mcp", Target: "jira/get_issue", Role: "implement"}); d.Kind != gate.Allow {
		t.Fatal(d)
	}
	// A read-only role gets nothing it was not given by name.
	if d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Ask, gate.Action{Kind: "mcp", Target: "jira/get_issue", Role: "review_final"}); d.Kind != gate.Ask {
		t.Fatal(d)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatal("native files changed without --native")
	}
	// The default rules are all still there: the learned rule adds to them.
	if d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Ask, gate.Action{Kind: "exec", Target: "git status", Role: "implement"}); d.Kind != gate.Allow {
		t.Fatal(d)
	}
	out, err = rememberIn(t, "--native", "--role", "review_final", "mcp:my-wiki/*")
	if err != nil || !strings.Contains(out, "claude: mcp__my-wiki") || !strings.Contains(out, "codex: no rule") {
		t.Fatal(out, err)
	}
	cfg, _ = config.Load(root)
	if d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Ask, gate.Action{Kind: "mcp", Target: "my_wiki/search", Role: "review_final"}); d.Kind != gate.Allow {
		t.Fatal(d)
	}
	out, err = rememberIn(t, "--native", "mcp:jira/get_*")
	if err != nil || !strings.Contains(out, "claude: no rule") {
		t.Fatal(out, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.local.json"))
	if strings.Contains(string(data), "get_*") || !strings.Contains(string(data), `"mcp__my-wiki"`) {
		t.Fatal(string(data))
	}
	if _, err = rememberIn(t, "mcp:jira"); err == nil {
		t.Fatal("a server with no tool was accepted")
	}
	// A glob or brace in the server has no Claude form, and the check of the
	// new rule uses a tool it covers.
	out, err = rememberIn(t, "--native", "mcp:{tracker,wiki}/read")
	if err != nil || !strings.Contains(out, "claude: no rule") {
		t.Fatal(out, err)
	}
	if out, err = rememberIn(t, "--native", "mcp:tracker*/*"); err != nil || !strings.Contains(out, "claude: no rule") {
		t.Fatal(out, err)
	}
	data, _ = os.ReadFile(filepath.Join(root, ".claude", "settings.local.json"))
	if strings.Contains(string(data), "tracker") || strings.Contains(string(data), "mcp___") {
		t.Fatal(string(data))
	}
}

// A whole server with __ in its name can be remembered: the check knows the
// server as written.
func TestRememberServerWithDoubleUnderscore(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if out, err := rememberIn(t, "--native", "mcp:my__wiki/*"); err != nil || !strings.Contains(out, "claude: mcp__my__wiki") {
		t.Fatal(out, err)
	}
}
