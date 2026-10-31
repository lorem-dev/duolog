//go:build e2e

// Package e2e runs the duolog binary against real Claude Code and Codex
// sessions. It needs both agents installed and logged in, and it spends tokens.
// Every test uses a fresh temporary project and cheap models at low effort.
// Run it with: go test -tags=e2e ./test/e2e/ -v
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// binDir holds the duolog binary built once for the whole run.
var binDir string

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	dir, err := os.MkdirTemp("", "duolog-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	name := "duolog"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, name), "./cmd/duolog")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		return 1
	}
	binDir = dir
	return m.Run()
}

// Cheap routes for every test. A role the test names here is the only one it
// uses, so a default role never sends work to an expensive model.
const projectConfig = `# yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json
version: 1
roles:
  e2e_codex:
    use: {agent: codex, model: luna, effort: low}
    fallback: {agent: claude, model: haiku, effort: low}
    sandbox: read-only
    context: minimal
    session: fresh
  e2e_claude:
    use: {agent: claude, model: haiku, effort: low}
    fallback: {agent: codex, model: luna, effort: low}
    sandbox: read-only
    context: minimal
    session: fresh
  e2e_claude_write:
    use: {agent: claude, model: haiku, effort: low}
    fallback: {agent: codex, model: luna, effort: low}
    sandbox: workspace-write
    context: minimal
    session: fresh
`

type project struct {
	t    *testing.T
	dir  string
	lead string // registered lead session id; delegate passes it with --lead
}

type result struct {
	Stdout, Stderr string
	Code           int
}

// newProject makes a temporary Git project with the cheap config.
func newProject(t *testing.T) *project {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &project{t: t, dir: dir}
	p.git("init", "-q")
	p.git("config", "user.email", "e2e@example.invalid")
	p.git("config", "user.name", "duolog e2e")
	p.write(".duolog/config.yaml", projectConfig)
	p.write("README.md", "e2e project\n")
	p.git("add", "-A")
	p.git("commit", "-q", "-m", "start")
	return p
}

// registerLead records an interactive Claude lead the way its SessionStart hook
// does, so that worker gates find a lead instead of failing closed.
func (p *project) registerLead() {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(binDir, "duolog"), "hook", "SessionStart", "--agent", "claude")
	cmd.Dir = p.dir
	cmd.Env = append(env(), "CLAUDE_CODE_ENTRYPOINT=cli")
	cmd.Stdin = strings.NewReader(`{"session_id":"e2e-lead","hook_event_name":"SessionStart"}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		p.t.Fatalf("register lead: %v\n%s", err, out)
	}
	p.lead = "e2e-lead"
}

func (p *project) path(rel string) string { return filepath.Join(p.dir, filepath.FromSlash(rel)) }

func (p *project) write(rel, content string) {
	p.t.Helper()
	path := p.path(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *project) git(args ...string) string {
	p.t.Helper()
	full := append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = p.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// env gives children the built binary first on PATH and no inherited duolog
// session variables, so a test never sees the lead that runs it.
func env() []string {
	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "DUOLOG_") {
			continue
		}
		if strings.EqualFold(key, "PATH") {
			kv = key + "=" + binDir + string(os.PathListSeparator) + os.Getenv(key)
		}
		out = append(out, kv)
	}
	return out
}

func (p *project) command(ctx context.Context, name string, args ...string) result {
	p.t.Helper()
	if name == "duolog" {
		name = filepath.Join(binDir, name)
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = p.dir
	cmd.Env = env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{Stdout: stdout.String(), Stderr: stderr.String()}
	if exit, ok := err.(*exec.ExitError); ok {
		r.Code = exit.ExitCode()
	} else if err != nil {
		p.t.Fatalf("%s: %v", name, err)
	}
	return r
}

func (p *project) run(name string, args ...string) result {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	return p.command(ctx, name, args...)
}

// duolog runs the built binary and fails the test unless it exits with 0.
func (p *project) duolog(args ...string) string {
	p.t.Helper()
	r := p.run("duolog", args...)
	if r.Code != 0 {
		p.t.Fatalf("duolog %s: exit %d\n%s%s", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
	}
	return r.Stdout
}

// taskText builds a task with the five sections in the required order.
func taskText(goal, read, write, limits, check string) string {
	return "## Goal\n" + goal + "\n\n## Read\n" + read + "\n\n## Write\n" + write +
		"\n\n## Limits\n" + limits + "\n\n## Check\n" + check + "\n"
}

// delegate writes task.md, delegates it and returns the single answer line.
func (p *project) delegate(role, task string) string {
	p.t.Helper()
	p.write("task.md", task)
	args := []string{"delegate", role, "--task", "task.md"}
	if p.lead != "" {
		args = append(args, "--lead", p.lead)
	}
	r := p.run("duolog", args...)
	line := strings.TrimSpace(r.Stdout)
	p.t.Logf("delegate %s: exit %d: %s", role, r.Code, line)
	if r.Code != 0 {
		p.t.Fatalf("delegate %s failed with exit %d\n%s", role, r.Code, r.Stderr)
	}
	p.printUsage()
	return line
}

type usageRow struct {
	Role   string `json:"role"`
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Input  int64  `json:"input"`
	Cached int64  `json:"cached"`
	Output int64  `json:"output"`
}

// usage reads .duolog/usage.ndjson, which delegate fills for every worker run.
func (p *project) usage() []usageRow {
	p.t.Helper()
	data, err := os.ReadFile(p.path(".duolog/usage.ndjson"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		p.t.Fatal(err)
	}
	var rows []usageRow
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row usageRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			p.t.Fatalf("usage line %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func (p *project) printUsage() {
	p.t.Helper()
	for _, row := range p.usage() {
		p.t.Logf("usage: role=%s agent=%s model=%s input=%d cached=%d output=%d", row.Role, row.Agent, row.Model, row.Input, row.Cached, row.Output)
	}
}

// luna prices, USD per million tokens, the same as the default config.
func lunaCost(row usageRow) float64 {
	return (float64(row.Input-row.Cached)*0.10 + float64(row.Cached)*0.01 + float64(row.Output)*0.50) / 1e6
}

func needAgent(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not on PATH", name)
		}
	}
}

// trustCodexFolder lets Codex load project agents in this test's folder. It is
// the only code here that touches the real ~/.codex. It saves a backup of
// config.toml, adds one [projects."<dir>"] entry and, in cleanup, removes
// exactly that entry. It then checks that the rest of the file is unchanged.
// Tests that need it are skipped unless DUOLOG_E2E_TRUST=1.
func trustCodexFolder(t *testing.T, dir string) {
	t.Helper()
	if os.Getenv("DUOLOG_E2E_TRUST") != "1" {
		t.Skip("set DUOLOG_E2E_TRUST=1 to let this test trust its folder in ~/.codex/config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "config.toml")
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	existed := err == nil
	backup := path + ".duolog-e2e.bak"
	if existed {
		if err := os.WriteFile(backup, original, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entry := "\n[projects." + strconv.Quote(dir) + "]\ntrust_level = \"trusted\"\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte{}, original...), entry...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v; backup is %s", path, err, backup)
			return
		}
		if strings.Count(string(current), entry) != 1 {
			t.Errorf("trust entry for %s not found exactly once; left %s alone, backup is %s", dir, path, backup)
			return
		}
		restored := strings.Replace(string(current), entry, "", 1)
		if existed && restored != string(original) {
			t.Errorf("%s changed during the test beyond our entry; left it alone, backup is %s", path, backup)
			return
		}
		if !existed {
			if restored != "" {
				t.Errorf("%s gained other content during the test; left it alone", path)
				return
			}
			_ = os.Remove(path)
			return
		}
		if err := os.WriteFile(path, []byte(restored), 0o600); err != nil {
			t.Errorf("restore %s: %v; backup is %s", path, err, backup)
			return
		}
		_ = os.Remove(backup)
	})
}

var codeWordPattern = regexp.MustCompile(`^[A-Z]+-[A-Z]+-[0-9]+$`)

func requireWord(t *testing.T, line, word string) {
	t.Helper()
	if !codeWordPattern.MatchString(word) {
		t.Fatalf("bad code word %q", word)
	}
	if !strings.Contains(line, word) {
		t.Errorf("answer line does not contain %s: %s", word, line)
	}
}
