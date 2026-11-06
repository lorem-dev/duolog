package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const goodAnswer = `{"done":true,"summary":"Implemented.","files":["a.go"],"needs":[]}`

func spec(t *testing.T, agent string) Spec {
	t.Helper()
	model, effort := "gpt-6-luna", "low"
	if agent == "claude" {
		model, effort = "opus", "high"
	}
	return rebuilt(t, Spec{Route: coreroute.Route{Target: config.Target{Agent: agent, Model: model, Effort: effort}, Sandbox: "read-only", Context: "minimal"}, Mode: mode.Plan, TaskDir: t.TempDir(), Cwd: t.TempDir(), Stdin: []byte("<stdin>\nImplement the task.\n</stdin>")})
}

// heldDisallowed is the --disallowedTools value of spec's Claude worker,
// which the gate holds read-only (mode.GateHeld).
var heldDisallowed = "--disallowedTools=Agent,Task," + strings.Join(mode.HeldDisallowedTools(false), ",")

// rebuilt builds s's free worker process again, after a test changed what
// the build reads: the context, the folder, the roots, the MCP env or PATH.
func rebuilt(t *testing.T, s Spec) Spec {
	t.Helper()
	return built(t, s, &config.Config{}, workerenv.Free)
}
func containsArgs(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("%v missing %q", got, w)
		}
	}
}
func TestCodexArgvMinimal(t *testing.T) {
	s := spec(t, "codex")
	a := Argv(s)
	containsArgs(t, a, "codex", "exec", "-m", "gpt-6-luna", `model_reasoning_effort="low"`, "project_doc_max_bytes=0", "skills.include_instructions=false", "allow_login_shell=false", "-s", "read-only", "--ephemeral", "-o", "--json", "Do the task in the stdin block.")
}
func TestCodexArgvProjectContext(t *testing.T) {
	s := spec(t, "codex")
	s.Route.Context = "project"
	if line := strings.Join(Argv(s), " "); strings.Contains(line, "project_doc_max_bytes") || strings.Contains(line, "skills.include_instructions") {
		t.Fatal(Argv(s))
	}
}

// A minimal-context worker gets its rules from the task, not from skills.
func TestClaudeArgvMinimalDropsSkills(t *testing.T) {
	s := spec(t, "claude")
	containsArgs(t, Argv(s), "--disable-slash-commands", "--settings", workerenv.ClaudeSettings(s.Cwd, true, nil))
	s.Route.Context = "project"
	s = rebuilt(t, s)
	if line := strings.Join(Argv(s), " "); strings.Contains(line, "--disable-slash-commands") || strings.Contains(line, "claudeMdExcludes") {
		t.Fatal(Argv(s))
	}
	// --setting-sources would also drop the user settings that enable the gate.
	if strings.Contains(strings.Join(Argv(s), " "), "--setting-sources") {
		t.Fatal(Argv(s))
	}
}

// Auto memory stays off in either context: its folder belongs to the project
// path, so a worker's note would reach the lead.
func TestClaudeArgvNoAutoMemory(t *testing.T) {
	s := spec(t, "claude")
	for _, context := range []string{"minimal", "project"} {
		s.Route.Context = context
		a := Argv(s)
		i := slices.Index(a, "--settings")
		if i < 0 || !strings.Contains(a[i+1], `"autoMemoryEnabled":false`) {
			t.Fatal(context, a)
		}
	}
}

// A worker gets MCP servers only when its task needs one.
func TestClaudeArgvMCPOnlyWhenNeeded(t *testing.T) {
	s := spec(t, "claude")
	containsArgs(t, Argv(s), "--strict-mcp-config")
	s.KeepMCP = true
	if strings.Contains(strings.Join(Argv(s), " "), "--strict-mcp-config") {
		t.Fatal(Argv(s))
	}
}

// A worker cannot delegate, so it starts without the tools that start subagents
// and never plans around them. The list option takes one value, MCP tools
// included.
func TestClaudeArgvDisallowsSubagents(t *testing.T) {
	s := spec(t, "claude")
	for _, disallowed := range [][]string{nil, {"mcp__jira__delete_issue"}} {
		s.MCP = &MCP{DisallowedTools: disallowed}
		var values []string
		for _, a := range Argv(s) {
			if v, ok := strings.CutPrefix(a, "--disallowedTools="); ok {
				values = append(values, v)
			}
		}
		if len(values) != 1 {
			t.Fatal(disallowed, Argv(s))
		}
		tools := strings.Split(values[0], ",")
		for _, want := range append([]string{"Agent", "Task"}, disallowed...) {
			if !slices.Contains(tools, want) {
				t.Fatal(want, values)
			}
		}
	}
	s.MCP = nil
	// spec's worker is a read-only role at level plan, held by the gate.
	containsArgs(t, Argv(s), heldDisallowed)
	s.Route.Sandbox, s.Mode = "workspace-write", mode.Edits
	containsArgs(t, Argv(rebuilt(t, s)), "--disallowedTools=Agent,Task")
	if line := strings.Join(Argv(spec(t, "codex")), " "); strings.Contains(line, "disallowedTools") || strings.Contains(line, "Agent") {
		t.Fatal(line)
	}
}
func TestCodexArgvResume(t *testing.T) {
	s := spec(t, "codex")
	s.ThreadID = "thread-123"
	a := Argv(s)
	// Model the CLI scopes: exec accepts sandbox; resume does not. Consume
	// option values too, so a misplaced flag cannot hide among positionals.
	resume := false
	var positional []string
	// The process prefix carries the global overrides; exec follows it.
	exec := len(s.Process.Prefix)
	if a[exec] != "exec" {
		t.Fatal(a)
	}
	for i := exec + 1; i < len(a); i++ {
		switch a[i] {
		case "resume":
			resume = true
		case "-s", "--sandbox":
			if resume {
				t.Fatalf("resume does not accept %s: %v", a[i], a)
			}
			i++
			if i >= len(a) || a[i] != "read-only" {
				t.Fatalf("missing sandbox value: %v", a)
			}
		case "-m", "-c", "-o":
			i++
			if i >= len(a) {
				t.Fatalf("missing option value: %v", a)
			}
		case "--json":
		default:
			if strings.HasPrefix(a[i], "-") || !resume {
				t.Fatalf("unexpected argument %q: %v", a[i], a)
			}
			positional = append(positional, a[i])
		}
	}
	if !resume || !reflect.DeepEqual(positional, []string{s.ThreadID, taskInstruction}) {
		t.Fatal(a)
	}
	containsArgs(t, a, "-s", "read-only")
}
func TestClaudeArgv(t *testing.T) {
	s := spec(t, "claude")
	containsArgs(t, Argv(s), "claude", "-p", "--model", "opus", "--effort", "high", "--output-format", "stream-json", "--verbose", "--json-schema", "--no-session-persistence")
	s.ThreadID = "session-123"
	a := Argv(s)
	containsArgs(t, a, "--resume", "session-123")
	if strings.Contains(strings.Join(a, " "), "--no-session-persistence") {
		t.Fatal(a)
	}
}
func install(t *testing.T, s *Spec, sc testbin.Scenario) string {
	t.Helper()
	p := testbin.Install(t, s.Route.Agent, sc)
	*s = rebuilt(t, *s)
	return p
}
func read(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func fixture(t *testing.T, name string) string {
	t.Helper()
	return string(read(t, filepath.Join("..", "..", "scripts", "bench", "fixtures", name)))
}
func TestStdoutGoesToLogFile(t *testing.T) {
	s := spec(t, "codex")
	out := strings.Repeat("x", 1024*1024) + "\n"
	install(t, &s, testbin.Scenario{Stdout: out, Stderr: "diagnostic\n", Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}})
	r, e := Run(context.Background(), s)
	if e != nil || !r.Done || r.Summary != "Implemented." {
		t.Fatalf("%+v %v", r, e)
	}
	// The two streams are copied concurrently, so stderr can land inside stdout
	// in the combined log. No byte may be lost, and each capture stays whole.
	log := string(read(t, filepath.Join(s.TaskDir, "stdout.log")))
	if strings.Count(log, "x") != 1024*1024 || !strings.Contains(log, "diagnostic") {
		t.Fatal("output missing")
	}
	if string(read(t, filepath.Join(s.TaskDir, "codex.stdout.json"))) != out {
		t.Fatal("stdout capture is not whole")
	}
	if string(read(t, filepath.Join(s.TaskDir, "stderr.log"))) != "diagnostic\n" {
		t.Fatal("stderr capture is not whole")
	}
	b, _ := json.Marshal(r)
	if len(b) > 1024 {
		t.Fatalf("result grew to %d", len(b))
	}
}
func TestUsageParsed(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			s := spec(t, agent)
			var sc testbin.Scenario
			want := Usage{Input: 30562, Cached: 23040, Output: 59}
			session := "00000000-0000-4000-8000-000000000002"
			if agent == "codex" {
				sc.Stdout = fixture(t, "results/codex-exec-events.jsonl")
				sc.Writes = map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}
			} else {
				var v map[string]json.RawMessage
				if e := json.Unmarshal([]byte(fixture(t, "results/claude-p-result.json")), &v); e != nil {
					t.Fatal(e)
				}
				v["structured_output"] = json.RawMessage(goodAnswer)
				b, _ := json.Marshal(v)
				sc.Stdout = string(b)
				// The recorded result wrote 4792 tokens to the one-hour cache.
				want = Usage{Input: 32831, Cached: 28035, Write: 4792, WriteHour: 4792, Output: 91}
				session = "00000000-0000-4000-8000-000000000043"
			}
			install(t, &s, sc)
			r, e := Run(context.Background(), s)
			if e != nil || r.Usage != want || r.SessionID != session || !r.Done {
				t.Fatalf("%+v %v; want %+v, %s", r, e, want, session)
			}
			if string(read(t, filepath.Join(s.TaskDir, "answer.json"))) != goodAnswer {
				t.Fatal("answer not saved")
			}
		})
	}
}
func TestQuotaDetected(t *testing.T) {
	s := spec(t, "codex")
	install(t, &s, testbin.Scenario{Exit: 1, Stderr: "usage limit"})
	r, e := Run(context.Background(), s)
	if e == nil || !r.QuotaEnded || r.Done || r.ExitCode != 1 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestRealClaudeLimitText(t *testing.T) {
	s := spec(t, "claude")
	install(t, &s, testbin.Scenario{Exit: 1, Stderr: fixture(t, "limits/claude-5h.txt")})
	before := time.Now()
	r, e := Run(context.Background(), s)
	if e == nil || !r.QuotaEnded || r.ResetAt.IsZero() {
		t.Fatalf("%+v %v", r, e)
	}
	loc, e := time.LoadLocation("Europe/London")
	if e != nil {
		t.Fatal(e)
	}
	now := before.In(loc)
	want := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, loc)
	if !want.After(now) {
		want = want.AddDate(0, 0, 1)
	}
	if !r.ResetAt.Equal(want) || r.ResetAt.Location().String() != "Europe/London" {
		t.Fatalf("%s want %s", r.ResetAt, want)
	}
}
func TestLimitWithoutResetTime(t *testing.T) {
	for text, wait := range map[string]time.Duration{"usage limit": time.Hour, "usage limit resets tomorrow": time.Hour, "usage limit resets 25am (Europe/London)": time.Hour, "usage limit resets 3am (Invalid/Zone)": time.Hour, "You've hit your 5-hour limit": 5 * time.Hour} {
		s := spec(t, "claude")
		install(t, &s, testbin.Scenario{Exit: 1, Stderr: text})
		before := time.Now()
		r, _ := Run(context.Background(), s)
		if !r.QuotaEnded || r.ResetAt.Before(before.Add(wait)) || r.ResetAt.After(time.Now().Add(wait)) {
			t.Fatalf("%q: %+v", text, r)
		}
	}
}
func TestOtherFailureNotQuota(t *testing.T) {
	s := spec(t, "codex")
	install(t, &s, testbin.Scenario{Exit: 1, Stderr: "panic"})
	r, e := Run(context.Background(), s)
	if e == nil || r.QuotaEnded || r.Done {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestMalformedAnswer(t *testing.T) {
	for _, answer := range []string{"not JSON", "{}", "null", `{"done":true}`, `{"done":true,"summary":"ok","files":null,"needs":[]}`} {
		s := spec(t, "codex")
		install(t, &s, testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): answer}})
		r, e := Run(context.Background(), s)
		if r.Done || e == nil || !strings.Contains(e.Error(), filepath.Join(s.TaskDir, "stdout.log")) {
			t.Fatalf("%+v %v", r, e)
		}
	}
}
func TestOnlyPassthroughEnvIsSet(t *testing.T) {
	s := spec(t, "codex")
	p := install(t, &s, testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}})
	t.Setenv("DUOLOG_PRIVATE_TEST", "secret")
	if _, e := Run(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	var env []string
	if e := json.Unmarshal(read(t, p+".env.json"), &env); e != nil {
		t.Fatal(e)
	}
	for _, v := range env {
		if strings.HasPrefix(v, "DUOLOG_PRIVATE_TEST=") {
			t.Fatal("parent env leaked")
		}
	}
	containsArgs(t, env, s.Process.Env...)
	containsArgs(t, env, "DUOLOG_DEPTH=1", "DUOLOG_LEAD=lead-123")
}
func TestStdinCarriesPrompt(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, block := range [][]byte{[]byte(strings.Repeat("task block\n", 10000)), nil} {
			s := spec(t, agent)
			sc := testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}}
			if agent == "claude" {
				sc.Stdout = `{"structured_output":` + goodAnswer + `}`
			}
			p := install(t, &s, sc)
			s.Stdin = block
			s.Prompt = "This must not go in argv"
			if _, e := Run(context.Background(), s); e != nil {
				t.Fatal(e)
			}
			if got := string(read(t, p+".stdin")); got != string(block) && got != string(block)+"\n"+codexAnswerInstruction+"\n" {
				t.Fatal("stdin changed")
			}
			a := string(read(t, p+".args.json"))
			if len(a) > 4096 || strings.Contains(a, s.Prompt) || !strings.Contains(a, "Do the task in the stdin block.") {
				t.Fatal(a)
			}
			gotDir, err := os.Stat(string(read(t, p+".cwd")))
			if err != nil {
				t.Fatal(err)
			}
			wantDir, err := os.Stat(s.Cwd)
			if err != nil || !os.SameFile(gotDir, wantDir) {
				t.Fatal("wrong cwd")
			}
		}
	}
}
func TestExitZeroWithoutAnswerIsNotDone(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		s := spec(t, agent)
		install(t, &s, testbin.Scenario{})
		r, e := Run(context.Background(), s)
		if r.Done || e == nil || !strings.Contains(e.Error(), filepath.Join(s.TaskDir, "stdout.log")) {
			t.Fatalf("%+v %v", r, e)
		}
	}
}
func TestSpawnDisablesUpdates(t *testing.T) {
	s := spec(t, "codex")
	containsArgs(t, Argv(s), "check_for_update_on_startup=false")
	s = spec(t, "claude")
	p := install(t, &s, testbin.Scenario{Stdout: `{"structured_output":` + goodAnswer + `}`})
	// The person's own values, even listed in env_passthrough, never win.
	t.Setenv("DISABLE_AUTOUPDATER", "0")
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "0")
	s = built(t, s, &config.Config{EnvPassthrough: []string{"DISABLE_AUTOUPDATER", "CLAUDE_CODE_DISABLE_AUTO_MEMORY"}}, workerenv.Free)
	if _, e := Run(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	var env []string
	if err := json.Unmarshal(read(t, p+".env.json"), &env); err != nil {
		t.Fatal(err)
	}
	// Auto memory is per project path: a worker's notes would reach the lead.
	containsArgs(t, env, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
	for _, v := range env {
		if v == "DISABLE_AUTOUPDATER=0" || v == "CLAUDE_CODE_DISABLE_AUTO_MEMORY=0" {
			t.Fatal(env)
		}
	}
}
func TestInterruptedRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix signal exit status")
	}
	s := spec(t, "codex")
	install(t, &s, testbin.Scenario{Exit: -1})
	r, e := Run(context.Background(), s)
	if e == nil || !r.Interrupted || r.QuotaEnded {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestHungRunTimesOut(t *testing.T) {
	s := spec(t, "codex")
	p := install(t, &s, testbin.Scenario{SleepMS: 60000, Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}})
	s.Timeout = 3 * time.Second
	start := time.Now()
	r, e := Run(context.Background(), s)
	// The fake sleeps a minute; the run must end at its 3 s timeout. Ending
	// the process tree took 2 s on a windows-latest runner, so the bound
	// leaves 15 s past the timeout and still proves the run did not wait.
	took := time.Since(start)
	if e == nil || !errors.Is(e, context.DeadlineExceeded) || !strings.Contains(e.Error(), filepath.Join(s.TaskDir, "stdout.log")) || took > s.Timeout+15*time.Second || r.Done || !r.Interrupted || r.QuotaEnded {
		t.Fatalf("%+v %v after %s", r, e, took)
	}
	if _, e := os.Stat(p + ".args.json"); e != nil {
		t.Fatal("fake did not start")
	}
	if _, e := os.Stat(filepath.Join(s.TaskDir, "answer.json")); !os.IsNotExist(e) {
		t.Fatal("sleeping process completed")
	}
}
func TestCanceledRun(t *testing.T) {
	s := spec(t, "codex")
	install(t, &s, testbin.Scenario{SleepMS: 60000})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, e := Run(ctx, s)
	if !errors.Is(e, context.Canceled) || !r.Interrupted || r.Done {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestReusedTaskDirDoesNotAcceptStaleAnswer(t *testing.T) {
	s := spec(t, "codex")
	install(t, &s, testbin.Scenario{})
	if e := os.WriteFile(filepath.Join(s.TaskDir, "answer.json"), []byte(goodAnswer), 0600); e != nil {
		t.Fatal(e)
	}
	r, e := Run(context.Background(), s)
	if e == nil || r.Done {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestAnswerCanReportUnfinishedTask(t *testing.T) {
	s := spec(t, "codex")
	answer := `{"done":false,"summary":"Need input.","files":[],"needs":["Specify target."]}`
	install(t, &s, testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): answer}})
	r, e := Run(context.Background(), s)
	if e != nil || r.Done || !reflect.DeepEqual(r.Needs, []string{"Specify target."}) {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestSuccessfulAnswerMentioningQuota(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			s := spec(t, agent)
			answer := `{"done":true,"summary":"Explained usage limit and rate_limit; resets 3am (Europe/London)","files":[],"needs":[]}`
			sc := testbin.Scenario{Stderr: "diagnostic: usage limit documentation\n"}
			if agent == "codex" {
				sc.Stdout = `{"type":"item.completed","item":{"type":"agent_message","text":"Explained usage limit"}}` + "\n" + `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"rate limit"}}`
				sc.Writes = map[string]string{filepath.Join(s.TaskDir, "answer.json"): answer}
			} else {
				sc.Stdout = `{"structured_output":` + answer + `}`
			}
			install(t, &s, sc)
			r, err := Run(context.Background(), s)
			if err != nil || !r.Done || r.QuotaEnded || !r.ResetAt.IsZero() {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestClaudeStderrDoesNotCorruptResult(t *testing.T) {
	for _, interleaved := range []bool{false, true} {
		t.Run(fmt.Sprint(interleaved), func(t *testing.T) {
			s := spec(t, "claude")
			stdout := `{"session_id":"session-123","usage":{"input_tokens":7},"structured_output":` + goodAnswer + `}`
			diagnostic := `{"diagnostic":"startup warning"}` + "\n"
			sc := testbin.Scenario{Output: []testbin.OutputChunk{{Stderr: true, Text: diagnostic}, {Text: stdout}}}
			if interleaved {
				mid := len(stdout) / 2
				sc.Output = []testbin.OutputChunk{{Text: stdout[:mid]}, {Stderr: true, Text: diagnostic}, {Text: stdout[mid:]}}
			}
			install(t, &s, sc)
			r, err := Run(context.Background(), s)
			if err != nil || !r.Done || r.SessionID != "session-123" || r.Usage.Input != 7 {
				t.Fatalf("%+v %v", r, err)
			}
			log := string(read(t, filepath.Join(s.TaskDir, "stdout.log")))
			if !strings.Contains(log, diagnostic) || !strings.Contains(log, stdout[:len(stdout)/2]) || !strings.Contains(log, stdout[len(stdout)/2:]) {
				t.Fatal("log is missing stdout or stderr")
			}
			if string(read(t, filepath.Join(s.TaskDir, "answer.json"))) != goodAnswer {
				t.Fatal("answer not saved")
			}
		})
	}
}

func TestQuotaFailureChannels(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, tc := range []struct {
			name, stdout, stderr string
			exit                 int
			quota                bool
		}{
			{name: "tool output on failure", exit: 1, stdout: `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"usage limit resets 3am (Europe/London)"}}`},
			{name: "model answer on failure", exit: 1, stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"usage limit resets 3am (Europe/London)"}}`},
			{name: "plain stdout on failure", exit: 1, stdout: "usage limit"},
			{name: "broad rate limit text", exit: 1, stderr: "panic in rate limiter"},
			{name: "successful diagnostic", stderr: "quota exceeded"},
			{name: "http status", exit: 1, stderr: "HTTP 429", quota: true},
			{name: "quota exceeded", exit: 1, stderr: "quota exceeded", quota: true},
			{name: "quota code", exit: 1, stderr: "insufficient_quota", quota: true},
			{name: "rate code", exit: 1, stderr: "rate_limit", quota: true},
			{name: "stdout error event", stdout: `{"type":"error","message":"usage limit resets 3am (Europe/London)"}`, quota: true},
			{name: "nested error event", exit: 1, stdout: `{"type":"error","error":{"code":"insufficient_quota","message":"quota exceeded"}}`, quota: true},
			{name: "stderr error event", stderr: `{"type":"error","message":"HTTP 429"}`, quota: true},
		} {
			t.Run(agent+"/"+tc.name, func(t *testing.T) {
				s := spec(t, agent)
				install(t, &s, testbin.Scenario{Exit: tc.exit, Stdout: tc.stdout, Stderr: tc.stderr})
				r, _ := Run(context.Background(), s)
				if r.QuotaEnded != tc.quota || !tc.quota && !r.ResetAt.IsZero() {
					t.Fatalf("quota=%v, want %v; result: %+v", r.QuotaEnded, tc.quota, r)
				}
				if tc.name == "stdout error event" && r.ResetAt.IsZero() {
					t.Fatal("error event reset time missing")
				}
			})
		}
	}
}

func TestClaudeLeadingStdoutDiagnostics(t *testing.T) {
	s := spec(t, "claude")
	var output any
	if err := json.Unmarshal([]byte(`{"session_id":"session-123","usage":{"input_tokens":7},"structured_output":`+goodAnswer+`}`), &output); err != nil {
		t.Fatal(err)
	}
	pretty, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := "Startup warning: using project settings\n{warning: connection delayed}\n\nConnecting to services...\n"
	install(t, &s, testbin.Scenario{Stdout: diagnostics + string(pretty), Stderr: "stderr diagnostic\n"})
	r, err := Run(context.Background(), s)
	if err != nil || !r.Done || r.SessionID != "session-123" || r.Usage.Input != 7 || r.Summary != "Implemented." {
		t.Fatalf("%+v %v", r, err)
	}
	var answer any
	if err := json.Unmarshal(read(t, filepath.Join(s.TaskDir, "answer.json")), &answer); err != nil || !reflect.DeepEqual(answer, output.(map[string]any)["structured_output"]) {
		t.Fatalf("saved answer: %v, %v", answer, err)
	}
	log := string(read(t, filepath.Join(s.TaskDir, "stdout.log")))
	if !strings.Contains(log, diagnostics) || !strings.Contains(log, string(pretty)) || !strings.Contains(log, "stderr diagnostic") {
		t.Fatal("log is missing stdout or stderr")
	}
}

func TestRealCodexLimitText(t *testing.T) {
	s := spec(t, "codex")
	install(t, &s, testbin.Scenario{Exit: 1, Stdout: fixture(t, "limits/codex-usage.jsonl")})
	before := time.Now()
	r, e := Run(context.Background(), s)
	if e == nil || !r.QuotaEnded || r.ResetAt.IsZero() {
		t.Fatalf("%+v %v", r, e)
	}
	want := time.Date(before.Year(), before.Month(), before.Day(), 8, 48, 0, 0, before.Location())
	if !want.After(before) {
		want = want.AddDate(0, 0, 1)
	}
	if !r.ResetAt.Equal(want) {
		t.Fatalf("%s want %s", r.ResetAt, want)
	}
}

func TestCodexArgvHasNoOutputSchema(t *testing.T) {
	for _, thread := range []string{"", "thread-1"} {
		s := spec(t, "codex")
		s.ThreadID = thread
		for _, a := range Argv(s) {
			if a == "--output-schema" {
				t.Fatalf("argv holds --output-schema: %v", Argv(s))
			}
		}
	}
	containsArgs(t, Argv(spec(t, "claude")), "--json-schema")
}

func TestCodexStdinEndsWithAnswerInstruction(t *testing.T) {
	s := spec(t, "codex")
	p := install(t, &s, testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}})
	if _, e := Run(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	got := string(read(t, p+".stdin"))
	if !strings.HasPrefix(got, string(s.Stdin)) || !strings.HasSuffix(strings.TrimSpace(got), codexAnswerInstruction) {
		t.Fatalf("stdin: %q", got)
	}
}

func TestCodexTolerantAnswer(t *testing.T) {
	last := `{"done":true,"summary":"second","files":["b"],"needs":[]}`
	cases := []struct {
		name, message, summary string
		done                   bool
	}{
		{"plain", goodAnswer, "Implemented.", true},
		{"fenced", "Here it is:\n```json\n" + goodAnswer + "\n```\n", "Implemented.", true},
		{"prose around", "Result " + goodAnswer + " thanks", "Implemented.", true},
		{"last valid wins", `{"done":false,"summary":"first","files":[],"needs":[]} then ` + last, "second", true},
		{"invalid later object ignored", last + ` {"done":true}`, "second", true},
		{"braces in summary", `{"done":true,"summary":"a } { b","files":[],"needs":[]}`, "a } { b", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := spec(t, "codex")
			install(t, &s, testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): c.message}})
			r, e := Run(context.Background(), s)
			if e != nil || r.Done != c.done || r.Summary != c.summary {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}

func TestCodexNoObjectGivesSummaryAndLogError(t *testing.T) {
	s := spec(t, "codex")
	message := "I could not do it.\n" + strings.Repeat("long ", 100)
	install(t, &s, testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): message}})
	r, e := Run(context.Background(), s)
	if e == nil || r.Done || !strings.Contains(e.Error(), filepath.Join(s.TaskDir, "stdout.log")) {
		t.Fatalf("%+v %v", r, e)
	}
	if strings.Contains(r.Summary, "\n") || len(r.Summary) > 200 || !strings.HasPrefix(r.Summary, "I could not do it. long") {
		t.Fatalf("summary %q", r.Summary)
	}
}

func TestClaudeJSONResultLimitTriggersQuota(t *testing.T) {
	s := spec(t, "claude")
	install(t, &s, testbin.Scenario{Exit: 1, Stdout: `{"type":"result","is_error":true,"result":"You've hit your usage limit"}`})
	got, err := Run(context.Background(), s)
	if err == nil || !got.QuotaEnded {
		t.Fatal(got, err)
	}
}

// A turn cap reaches Claude as --max-turns; Codex has no such flag, so its
// argv is unchanged.
func TestMaxTurnsArgv(t *testing.T) {
	s := spec(t, "claude")
	s.MaxTurns = 7
	containsArgs(t, Argv(s), "--max-turns", "7")
	s.MaxTurns = 0
	if slices.Contains(Argv(s), "--max-turns") {
		t.Fatal(Argv(s))
	}
	c := spec(t, "codex")
	without := Argv(c)
	c.MaxTurns = 7
	if !reflect.DeepEqual(Argv(c), without) {
		t.Fatalf("codex argv with a turn cap %v, want %v", Argv(c), without)
	}
}

// Claude ends a run at --max-turns with an error_max_turns result and no
// answer. Run reports it as TurnsEnded with the usage, whatever the exit.
func TestClaudeTurnCapEndsRun(t *testing.T) {
	for _, exit := range []int{0, 1} {
		s := spec(t, "claude")
		s.MaxTurns = 2
		install(t, &s, testbin.Scenario{Exit: exit, Stdout: `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":3,"session_id":"worker-1","usage":{"input_tokens":30,"cache_read_input_tokens":10,"output_tokens":5}}` + "\n"})
		got, err := Run(context.Background(), s)
		if !errors.Is(err, ErrTurnCap) || !got.TurnsEnded || got.Done || got.QuotaEnded {
			t.Fatalf("exit %d: %+v %v, want TurnsEnded and ErrTurnCap", exit, got, err)
		}
		if got.Usage != (Usage{Input: 40, Cached: 10, Output: 5}) || !got.UsageKnown {
			t.Fatalf("exit %d: usage %+v known %v, want 40 input, 10 cached, 5 output, known", exit, got.Usage, got.UsageKnown)
		}
	}
	// Cache writes without a split by cache lifetime count as five-minute writes.
	s := spec(t, "claude")
	s.MaxTurns = 2
	install(t, &s, testbin.Scenario{Exit: 1, Stdout: `{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"worker-1","usage":{"input_tokens":30,"cache_creation_input_tokens":20,"cache_read_input_tokens":10,"output_tokens":5}}` + "\n"})
	got, err := Run(context.Background(), s)
	if !errors.Is(err, ErrTurnCap) || got.Usage != (Usage{Input: 60, Cached: 10, Write: 20, Output: 5}) || !got.UsageKnown {
		t.Fatalf("cache writes: usage %+v known %v %v, want 60 input, 10 cached, 20 five-minute writes", got.Usage, got.UsageKnown, err)
	}
	// A capped run without usage is told from one with zero usage.
	s = spec(t, "claude")
	s.MaxTurns = 2
	install(t, &s, testbin.Scenario{Exit: 1, Stdout: `{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"worker-1"}` + "\n"})
	got, err = Run(context.Background(), s)
	if !errors.Is(err, ErrTurnCap) || !got.TurnsEnded || got.UsageKnown || got.Usage != (Usage{}) {
		t.Fatalf("without usage: %+v %v, want TurnsEnded with usage unknown", got, err)
	}
}

func TestFinishedWorkerSurvivesDescendantHoldingOutput(t *testing.T) {
	s := spec(t, "claude")
	fake := install(t, &s, testbin.Scenario{SpawnChild: true, Stdout: `{"structured_output":` + goodAnswer + `}`})
	t.Cleanup(func() {
		data, err := os.ReadFile(fake + ".child-pid")
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return
		}
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	got, err := Run(context.Background(), s)
	if err != nil || got.Interrupted || !got.Done {
		t.Fatal(got, err)
	}
}

func TestCancellationKillsDescendantHoldingOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows process-tree verification pending")
	}
	s := spec(t, "claude")
	s.Timeout = time.Minute
	fake := install(t, &s, testbin.Scenario{SpawnChild: true, SleepMS: 60000})
	// Cancel once the descendant runs. A fixed short timeout could expire
	// before a loaded machine started it, and the test then saw no child.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan time.Time, 1)
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(fake + ".child-pid"); err == nil {
				cancelled <- time.Now()
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	got, err := Run(ctx, s)
	var since time.Duration
	select {
	case at := <-cancelled:
		since = time.Since(at)
	default:
		t.Fatal("the worker ended before its descendant started", got, err)
	}
	// The descendant holds the output for a minute; 15 s after the cancel
	// leaves a slow runner room and still proves the run did not wait for it.
	if err == nil || !got.Interrupted || since > 15*time.Second {
		t.Fatal(got, err, since)
	}
	pidData, pidErr := os.ReadFile(fake + ".child-pid")
	if pidErr != nil {
		t.Fatal("descendant never started", pidErr)
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if p, e := os.FindProcess(pid); e == nil {
			_ = p.Kill()
		}
	})
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(fake + ".child-done"); !os.IsNotExist(err) {
		t.Fatal("descendant survived cancellation", err)
	}
}

// A minimal worker whose task needs a skill keeps the skill list.
func TestMinimalKeepsSkillsForASkillNeed(t *testing.T) {
	s := spec(t, "claude")
	s.KeepSkills = true
	line := strings.Join(Argv(s), " ")
	if strings.Contains(line, "--disable-slash-commands") || !strings.Contains(line, "claudeMdExcludes") {
		t.Fatal(line)
	}
	s = spec(t, "codex")
	s.KeepSkills = true
	if line := strings.Join(Argv(s), " "); strings.Contains(line, "skills.include_instructions") || !strings.Contains(line, "project_doc_max_bytes=0") {
		t.Fatal(line)
	}
}

// Both agents return the worker's own decisions; an answer without them, from a
// worker of an older thread, still parses.
func TestAnswerKeepsDecisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answer.json")
	r, err := codexAnswer([]byte("Here:\n"+`{"done":true,"summary":"s","files":[],"needs":[],"decisions":["used slog"]}`), path, Result{})
	if err != nil || strings.Join(r.Decisions, ",") != "used slog" || !strings.Contains(string(read(t, path)), `"decisions":["used slog"]`) {
		t.Fatal(r, err)
	}
	if r, err = codexAnswer([]byte(goodAnswer), path, Result{}); err != nil || r.Decisions != nil || string(read(t, path)) != goodAnswer {
		t.Fatal(r, err)
	}
	if !strings.Contains(answerSchema, `"decisions"`) || !strings.Contains(codexAnswerInstruction, "decisions") {
		t.Fatal("the answer format misses decisions")
	}
}
