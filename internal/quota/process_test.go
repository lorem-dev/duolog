package quota

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	inheritedMarker = "inherited-marker-value"
	droppedSecret   = "sk-ant-dropped-secret"
)

// freeClaude is the reply key of a read by the compatibility Read: the free
// Claude worker's --settings prefix, then args.
func freeClaude(args string) string {
	return "--settings " + workerenv.ClaudeSettings("", false, nil) + " " + args
}

// recordedCalls reads the fake's argument log, one call per line.
func recordedCalls(t *testing.T, bin string) [][]string {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, args)
	}
	return calls
}

// windowsAdded are the names a Windows child gets without its parent: the
// SYSTEMROOT that Go adds, and the processor names that Windows on Arm sets
// for a child, such as one it runs under x64 emulation.
var windowsAdded = []string{"SYSTEMROOT", "PROCESSOR_ARCHITECTURE", "PROCESSOR_ARCHITEW6432"}

// sameEnv checks that the fake's last call saw exactly env: every pair and
// nothing else, except the names in windowsAdded on Windows.
func sameEnv(t *testing.T, bin string, env []string) {
	t.Helper()
	data, err := os.ReadFile(bin + ".env.json")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	added := func(name string) bool {
		return runtime.GOOS == "windows" && slices.ContainsFunc(windowsAdded, func(n string) bool { return strings.EqualFold(n, name) })
	}
	for _, pair := range got {
		name, _, _ := strings.Cut(pair, "=")
		if !slices.Contains(env, pair) && !added(name) {
			t.Errorf("fake saw %s, which the process does not set", name)
		}
	}
	for _, pair := range env {
		if !slices.Contains(got, pair) {
			name, _, _ := strings.Cut(pair, "=")
			t.Errorf("fake lacks %s", name)
		}
	}
	for _, value := range []string{inheritedMarker, droppedSecret} {
		if strings.Contains(string(data), value) {
			t.Error("an inherited or dropped value reached the fake")
		}
	}
}

// Every read runs the built process: its argv with the actual prefix and
// settings, exactly its environment, and its PATH to find the CLI. Nothing
// of duolog's own environment reaches the fake, and errors carry no values.
func TestQuotaReadUsesBuiltProcess(t *testing.T) {
	isolate(t)
	home := os.Getenv("HOME")
	t.Setenv("DUOLOG_INHERITED_MARKER", inheritedMarker)
	t.Setenv("ANTHROPIC_API_KEY", droppedSecret)
	cfg := &config.Config{EnvPassthrough: []string{"HOME", "USERPROFILE", "SYSTEMROOT", "VISIBLE", "ANTHROPIC_API_KEY"}}
	// build makes the free worker of agent whose PATH is only dir.
	build := func(agent, dir string) workerenv.Process {
		t.Helper()
		environ := []string{"PATH=" + dir, "HOME=" + home, "USERPROFILE=" + home, "VISIBLE=yes", "ANTHROPIC_API_KEY=" + droppedSecret}
		if root, ok := os.LookupEnv("SYSTEMROOT"); ok {
			environ = append(environ, "SYSTEMROOT="+root)
		}
		p, err := workerenv.Build(workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: workerenv.Free,
			Config: cfg, Environ: environ, LeadID: "lead-1"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// hide leaves duolog's own PATH without any fake.
	hide := func() { t.Setenv("PATH", t.TempDir()) }
	r := Reader{Version: "test", Timeout: 3 * time.Second}
	usage := []string{"-p", "/usage", "--output-format", "stream-json", "--verbose", "--no-session-persistence"}
	auth := []string{"auth", "status", "--json"}

	// Claude: /usage and auth status, both under the --settings prefix. The
	// settings do not depend on PATH, so a first build names the replies.
	prefix := strings.Join(build("claude", "").Argv()[1:], " ")
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		prefix + " -p":                 {Stdout: claudeReply + "\n"},
		prefix + " auth status --json": {Stdout: `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`},
	}})
	hide()
	p := build("claude", filepath.Dir(claude))
	s, err := r.ReadProcess(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Windows) != 2 || s.Plan != "max" || s.Billing.AuthMethod != "claude.ai" || s.ReadAt.IsZero() {
		t.Fatalf("%+v", s)
	}
	calls := recordedCalls(t, claude)
	if len(calls) != 2 || !slices.Equal(calls[0], p.Argv(usage...)[1:]) || !slices.Equal(calls[1], p.Argv(auth...)[1:]) {
		t.Fatalf("calls = %q", calls)
	}
	if calls[0][0] != "--settings" || calls[0][1] != p.Settings {
		t.Fatalf("usage read lacks the process settings: %q", calls[0])
	}
	sameEnv(t, claude, p.Env) // auth status, the last call

	// A failed /usage that printed no report reads auth status too, through
	// the same process. The login here cannot be read, so that is the answer
	// and the read is not tried again. Its stderr and exit never reach the
	// error.
	failing := testbin.Install(t, "claude", testbin.Scenario{Stderr: droppedSecret + " " + inheritedMarker, Exit: 3})
	hide()
	pf := build("claude", filepath.Dir(failing))
	_, err = r.ReadProcess(context.Background(), pf)
	if err == nil || strings.Contains(err.Error(), droppedSecret) || strings.Contains(err.Error(), inheritedMarker) {
		t.Fatalf("failing read: %v", err)
	}
	if calls := recordedCalls(t, failing); len(calls) != 2 || !slices.Equal(calls[0], pf.Argv(usage...)[1:]) || !slices.Equal(calls[1], pf.Argv(auth...)[1:]) {
		t.Fatalf("failing calls = %q", calls)
	}
	sameEnv(t, failing, pf.Env)

	// The CLI is looked up in the built PATH, not in duolog's own: a process
	// whose PATH lacks it reads nothing, even when duolog's PATH has it.
	t.Setenv("PATH", filepath.Dir(claude))
	_, err = r.ReadProcess(context.Background(), build("claude", t.TempDir()))
	if err == nil || err.Error() != "not installed" {
		t.Fatalf("lookup in duolog's PATH: %v", err)
	}

	// Codex: the four RPC steps over the built prefix. A notification and a
	// reply to another id never answer a request, and nothing spends a
	// reset credit.
	notify := `{"jsonrpc":"2.0","method":"account/rateLimits/updated","params":{"rateLimits":{"primary":{"usedPercent":99,"windowDurationMins":300,"resetsAt":4070908800}}}}`
	codex := testbin.Install(t, "codex", testbin.Scenario{RPCResponses: codexRoute(map[string]string{
		"initialize":              `{"id":1,"result":{}}`,
		"account/read":            notify + "\n" + `{"id":2,"result":{"rateLimits":null}}` + "\n" + codexAccountReply,
		"account/rateLimits/read": notify + "\n" + codexReply,
	})})
	hide()
	pc := build("codex", filepath.Dir(codex))
	s, err = r.ReadProcess(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Windows) != 2 || s.Windows[0].UsedPercent != 13 || s.Billing.AccountType != "chatgpt" {
		t.Fatalf("%+v", s)
	}
	if calls := recordedCalls(t, codex); len(calls) != 1 || !slices.Equal(calls[0], pc.Argv("app-server")[1:]) {
		t.Fatalf("codex calls = %q, want %q", calls, pc.Argv("app-server")[1:])
	}
	sameEnv(t, codex, pc.Env)
	input, _ := os.ReadFile(codex + ".stdin")
	var methods []string
	for _, line := range strings.Split(strings.TrimSpace(string(input)), "\n") {
		var msg struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, msg.Method)
	}
	if strings.Join(methods, ",") != codexReadMethods {
		t.Fatalf("methods = %v", methods)
	}
	for _, method := range methods {
		if lower := strings.ToLower(method); strings.Contains(lower, "reset") || strings.Contains(lower, "consume") {
			t.Errorf("read sent %s", method)
		}
	}

	// The compatibility Read builds the free worker itself from duolog's
	// environment: the marker and the dropped key stay out.
	t.Setenv("PATH", filepath.Dir(codex))
	if _, err := r.Read(context.Background(), "codex"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(codex + ".env.json")
	if strings.Contains(string(data), inheritedMarker) || strings.Contains(string(data), droppedSecret) {
		t.Error("Read passed duolog's whole environment")
	}
	if !strings.Contains(string(data), `"DUOLOG_DEPTH=1"`) {
		t.Errorf("Read lacks the worker depth: %s", data)
	}
}

// Claude names the $200 plan "max" and tells it apart only by planTier, so
// the read maps max with the 20x tier to ultra and keeps every other pair.
func TestClaudePlanReadsTheTwentyTimesTier(t *testing.T) {
	for _, c := range []struct{ sub, tier, want string }{
		{"max", "max_20x", "ultra"},
		{"max", "max_5x", "max"},
		{"max", "", "max"},
		{"pro", "max_20x", "pro"},
		{"ultra", "", "ultra"},
		{"nonsense", "max_20x", ""},
	} {
		if got := claudePlan(c.sub, c.tier); got != c.want {
			t.Errorf("claudePlan(%q, %q) = %q, want %q", c.sub, c.tier, got, c.want)
		}
	}
	isolate(t)
	testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p"):                 {Stdout: "{\"type\":\"system\"}\n" + claudeReply + "\n"},
		freeClaude("auth status --json"): {Stdout: `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max","planTier":"max_20x"}`},
	}})
	s, err := (Reader{Version: "test", Timeout: 3 * time.Second}).Read(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if s.Plan != "ultra" {
		t.Fatalf("plan = %q, want ultra", s.Plan)
	}
}
