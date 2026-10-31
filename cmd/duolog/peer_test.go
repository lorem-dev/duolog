package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const peerSplitReply = `{"anchor_id":"block:lead","block_ids":["block:peer"],"split_ids":["split:s"],"revision":4}`

func startupFixture(t *testing.T, name string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "scripts", "bench", "fixtures", "startup", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func claudeIdleScreen() string {
	rule := strings.Repeat("─", 64)
	return "\n" + rule + "\n❯ Try \"how does <filepath> work?\"\n" + rule + "\n  ⏸ manual mode on · ? for shortcuts\n"
}

func rexCalls(t *testing.T, bin string) [][]string {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var words []string
		if err := json.Unmarshal([]byte(line), &words); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, words)
	}
	return calls
}

// peerProject makes a project root with a live lead and a fake rex.
func peerProject(t *testing.T, capture testbin.Scenario) (root, rex string) {
	if runtime.GOOS == "windows" {
		t.Skip("live peer sessions are unavailable on Windows")
	}
	t.Helper()
	isolatedRouteConfig(t)
	root = t.TempDir()
	t.Chdir(root)
	t.Setenv("REX_SESSION", "sess")
	t.Setenv("REX_BLOCK", "")
	t.Setenv("REX_BIN_DIR", "")
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "lead-1")
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-1", Target: config.Target{Agent: "codex", Model: "sol", Effort: "high"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	rex = testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Stderr: "unexpected rex command", Responses: map[string]testbin.Scenario{
		"split":         {Stdout: peerSplitReply},
		"block inspect": {Stdout: "{}"},
		"wait":          {Exit: 1, Stderr: "Error: wait timed out after 500ms: context deadline exceeded"},
		"capture":       capture,
		"send":          {},
		"send-key":      {},
		"block close":   {},
		"block ls":      {Stdout: `{"blocks":[{"id":"block:peer"}]}`},
	}})
	return root, rex
}

func sequence(screens ...string) testbin.Scenario {
	var seq []testbin.Scenario
	for _, s := range screens {
		seq = append(seq, testbin.Scenario{Stdout: s})
	}
	return testbin.Scenario{Sequence: seq}
}

func TestPeerOpenRefusedForWorker(t *testing.T) {
	_, rex := peerProject(t, testbin.Scenario{})
	t.Setenv("DUOLOG_DEPTH", "1")
	_, err := executeBusCommand(t, "peer", "open", "plan")
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatalf("%v", err)
	}
	if len(rexCalls(t, rex)) != 0 {
		t.Fatal("a worker reached rex")
	}
}

func TestAnswerRefusedForWorker(t *testing.T) {
	_, rex := peerProject(t, testbin.Scenario{})
	t.Setenv("DUOLOG_DEPTH", "1")
	_, err := executeBusCommand(t, "peer", "answer", "peer-x", "Yes")
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatalf("%v", err)
	}
	if len(rexCalls(t, rex)) != 0 {
		t.Fatal("a worker reached rex")
	}
}

func TestOutsideRexIsError(t *testing.T) {
	_, rex := peerProject(t, testbin.Scenario{})
	t.Setenv("REX_SESSION", "")
	_, err := executeBusCommand(t, "peer", "open", "plan")
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "REX_SESSION") {
		t.Fatalf("%v", err)
	}
	if len(rexCalls(t, rex)) != 0 {
		t.Fatal("rex ran outside a session")
	}
}

// Every peer command prints one line and needs nothing but the command line.
func TestPeerOnPlainCLI(t *testing.T) {
	switchModel := startupFixture(t, "claude-model-switch-question.txt")
	_, rex := peerProject(t, sequence(switchModel, switchModel, switchModel, claudeIdleScreen(), claudeIdleScreen()))
	oneLine := func(args ...string) string {
		t.Helper()
		out, err := executeBusCommand(t, args...)
		if err != nil || strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
			t.Fatalf("%v: %q %v", args, out, err)
		}
		return strings.TrimSpace(out)
	}
	opened := oneLine("peer", "open", "plan")
	id, rest, _ := strings.Cut(opened, " ")
	if !strings.HasPrefix(id, "peer-") || !strings.HasPrefix(rest, "question: Switch model?") {
		t.Fatalf("open: %q", opened)
	}
	if status := oneLine("peer", "status", id); !strings.Contains(status, "question") {
		t.Fatalf("status: %q", status)
	}
	if answered := oneLine("peer", "answer", id, "Yes"); answered != id+" ready" {
		t.Fatalf("answer: %q", answered)
	}
	if closed := oneLine("peer", "close", id); closed != id+" closed" {
		t.Fatalf("close: %q", closed)
	}
	if again := oneLine("peer", "close", id); again != id+" closed" {
		t.Fatalf("a second close: %q", again)
	}
	if _, err := executeBusCommand(t, "peer", "send", id, "/model sonnet"); err == nil {
		t.Fatal("a slash command was sent")
	}
	for _, c := range rexCalls(t, rex) {
		if c[0] == "send" {
			t.Fatalf("text reached the pane: %v", c)
		}
	}
	for _, sub := range newPeerCommand().Commands() {
		if strings.Contains(strings.ToLower(sub.Name()), "mcp") {
			t.Fatal("peer depends on MCP")
		}
	}
}

func TestPeerSendOnLimitPrintsRunHere(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen()))
	opened, err := executeBusCommand(t, "peer", "open", "plan")
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := strings.Cut(strings.TrimSpace(opened), " ")
	if id == "" {
		t.Fatalf("%q", opened)
	}
	taskPath := filepath.Join(root, "task.md")
	body := "## Goal\nx\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nnone\n\n## Check\nnone\n"
	if err := os.WriteFile(taskPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rex := testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Responses: map[string]testbin.Scenario{
		"block inspect": {Stdout: "{}"},
		"wait":          {Exit: 1, Stderr: "Error: wait timed out after 500ms: context deadline exceeded"},
		"capture":       {Stdout: "You've hit your 5-hour limit · resets 3am (Europe/London)\n" + claudeIdleScreen()},
	}})
	out, err := executeBusCommand(t, "peer", "send", id, taskPath)
	if err != nil || !strings.HasPrefix(out, "run here: ") || strings.Count(out, "\n") != 1 {
		t.Fatalf("%q %v", out, err)
	}
	for _, c := range rexCalls(t, rex) {
		if c[0] == "send" || c[0] == "split" || c[0] == "send-key" {
			t.Fatalf("rex %v ran on a limit", c)
		}
	}
}

// The pane list in doctor comes from real peer entries: the join key is
// Entry.Block, and the entry ID is not a pane.
func TestDoctorListsPanesOfEndedLeadFromPeerOpen(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen()))
	opened, err := executeBusCommand(t, "peer", "open", "plan")
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := strings.Cut(strings.TrimSpace(opened), " ")
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reg.Entries()
	if err != nil || len(entries) != 1 || entries[0].ID != id || entries[0].Block != "block:peer" {
		t.Fatalf("%+v %v", entries, err)
	}
	if err := reg.Close("lead-1"); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Pane block:peer belongs to ended lead lead-1; duolog peer close block:peer") {
		t.Fatal(out)
	}
	// The command doctor prints works.
	if out, err := executeBusCommand(t, "peer", "close", "block:peer"); err != nil || strings.TrimSpace(out) != "block:peer closed" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestPeerLaunchSetsEnvWithoutRex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("peer launcher is unused on Windows")
	}
	program := testbin.Install(t, "agent", testbin.Scenario{})
	t.Setenv("REX_SESSION", "sess")
	t.Setenv("REX_BLOCK", "block:lead")
	t.Setenv("UNLISTED_SECRET", "secret")
	t.Setenv("TERM", "xterm")
	file := filepath.Join(t.TempDir(), "env.json")
	if err := os.WriteFile(file, []byte(`["DUOLOG_DEPTH=1","DUOLOG_LEAD=lead-1","API_KEY=listed"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeBusCommand(t, "peer", "launch", "--env-file", file, "--", program, "-m", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the launcher left the variables on disk", err)
	}
	var env []string
	data, err := os.ReadFile(program + ".env.json")
	if err != nil || json.Unmarshal(data, &env) != nil {
		t.Fatalf("%v %s", err, data)
	}
	have := map[string]string{}
	for _, pair := range env {
		name, value, _ := strings.Cut(pair, "=")
		have[name] = value
	}
	if have["DUOLOG_DEPTH"] != "1" || have["DUOLOG_LEAD"] != "lead-1" || have["API_KEY"] != "listed" || have["TERM"] != "xterm" {
		t.Fatalf("%v", have)
	}
	if _, ok := have["UNLISTED_SECRET"]; ok {
		t.Fatal("a variable outside env_passthrough reached the program")
	}
	if _, ok := have["REX_SESSION"]; ok {
		t.Fatal("REX_SESSION reached the program")
	}
	if _, ok := have["REX_BLOCK"]; ok {
		t.Fatal("REX_BLOCK reached the program")
	}
	args, _ := os.ReadFile(program + ".args.json")
	if string(args) != `["-m","x"]` {
		t.Fatalf("%s", args)
	}
	if _, err := executeBusCommand(t, "peer", "launch", "--env-file", file); err == nil {
		t.Fatal("launch without a program")
	}
}
