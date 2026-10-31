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
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const goodAnswer = `{"done":true,"summary":"Implemented.","files":["a.go"],"needs":[]}`

func spec(t *testing.T, agent string) Spec {
	t.Helper()
	model, effort := "gpt-6-luna", "low"
	if agent == "claude" {
		model, effort = "opus", "high"
	}
	return Spec{Route: router.Route{Target: config.Target{Agent: agent, Model: model, Effort: effort}, Sandbox: "read-only", Context: "minimal"}, Mode: mode.Plan, TaskDir: t.TempDir(), Cwd: t.TempDir(), Env: []string{"PATH=" + os.Getenv("PATH")}, Stdin: []byte("<stdin>\nImplement the task.\n</stdin>")}
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
	containsArgs(t, a, "codex", "exec", "-m", "gpt-6-luna", `model_reasoning_effort="low"`, "project_doc_max_bytes=0", "-s", "read-only", "--ephemeral", "-o", "--json", "Do the task in the stdin block.")
}
func TestCodexArgvProjectContext(t *testing.T) {
	s := spec(t, "codex")
	s.Route.Context = "project"
	if strings.Contains(strings.Join(Argv(s), " "), "project_doc_max_bytes") {
		t.Fatal(Argv(s))
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
	for i := 2; i < len(a); i++ {
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
	s.Allowed = []string{"Read", "Bash(go test:*)"}
	s.Disallowed = []string{"Write"}
	containsArgs(t, Argv(s), "claude", "-p", "--model", "opus", "--effort", "high", "--output-format", "json", "--json-schema", "--no-session-persistence", "--allowedTools", "Read", "Bash(go test:*)", "--disallowedTools", "Write")
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
	s.Env = []string{"PATH=" + os.Getenv("PATH")}
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
	log := string(read(t, filepath.Join(s.TaskDir, "stdout.log")))
	if !strings.Contains(log, out) || !strings.Contains(log, "diagnostic") {
		t.Fatal("output missing")
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
			session := "01a18f06-053d-71c0-9c12-2ff4bc24c280"
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
				want = Usage{Input: 32831, Cached: 28035, Output: 91}
				session = "f169ce46-23e8-44c8-8be4-8589e3f26d24"
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
	loc, e := time.LoadLocation("Europe/Belgrade")
	if e != nil {
		t.Fatal(e)
	}
	now := before.In(loc)
	want := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, loc)
	if !want.After(now) {
		want = want.AddDate(0, 0, 1)
	}
	if !r.ResetAt.Equal(want) || r.ResetAt.Location().String() != "Europe/Belgrade" {
		t.Fatalf("%s want %s", r.ResetAt, want)
	}
}
func TestLimitWithoutResetTime(t *testing.T) {
	for _, text := range []string{"usage limit", "usage limit resets tomorrow", "usage limit resets 25am (Europe/Belgrade)", "usage limit resets 3am (Invalid/Zone)"} {
		s := spec(t, "claude")
		install(t, &s, testbin.Scenario{Exit: 1, Stderr: text})
		r, _ := Run(context.Background(), s)
		if !r.QuotaEnded || !r.ResetAt.IsZero() {
			t.Fatalf("%+v", r)
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
	s.Env = append(s.Env, "DUOLOG_DEPTH=1", "DUOLOG_LEAD=lead-123")
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
	containsArgs(t, env, s.Env...)
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
	s.Env = append(s.Env, "DISABLE_AUTOUPDATER=0")
	if _, e := Run(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	var env []string
	json.Unmarshal(read(t, p+".env.json"), &env)
	containsArgs(t, env, "DISABLE_AUTOUPDATER=1")
	for _, v := range env {
		if v == "DISABLE_AUTOUPDATER=0" {
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
	if e == nil || !errors.Is(e, context.DeadlineExceeded) || !strings.Contains(e.Error(), filepath.Join(s.TaskDir, "stdout.log")) || time.Since(start) > 5*time.Second || r.Done || !r.Interrupted || r.QuotaEnded {
		t.Fatalf("%+v %v", r, e)
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
			answer := `{"done":true,"summary":"Explained usage limit and rate_limit; resets 3am (Europe/Belgrade)","files":[],"needs":[]}`
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
			{name: "tool output on failure", exit: 1, stdout: `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"usage limit resets 3am (Europe/Belgrade)"}}`},
			{name: "model answer on failure", exit: 1, stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"usage limit resets 3am (Europe/Belgrade)"}}`},
			{name: "plain stdout on failure", exit: 1, stdout: "usage limit"},
			{name: "broad rate limit text", exit: 1, stderr: "panic in rate limiter"},
			{name: "successful diagnostic", stderr: "quota exceeded"},
			{name: "http status", exit: 1, stderr: "HTTP 429", quota: true},
			{name: "quota exceeded", exit: 1, stderr: "quota exceeded", quota: true},
			{name: "quota code", exit: 1, stderr: "insufficient_quota", quota: true},
			{name: "rate code", exit: 1, stderr: "rate_limit", quota: true},
			{name: "stdout error event", stdout: `{"type":"error","message":"usage limit resets 3am (Europe/Belgrade)"}`, quota: true},
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
