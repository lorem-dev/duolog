package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/task"
	"github.com/lorem-dev/duolog/internal/testbin"
)

type harness struct {
	t          *testing.T
	root, path string
	cfg        *config.Config
	s          *store.Store
	reg        *session.Registry
	calls      []runner.Spec
	results    []runner.Result
	errs       []error
	change     string
}

func put(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func read(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func newHarness(t *testing.T) *harness {
	t.Helper()
	_, testFile, _, _ := runtime.Caller(0)
	var catalog struct {
		Models []models.Entry `json:"models"`
	}
	if e := json.Unmarshal([]byte(read(t, filepath.Join(filepath.Dir(testFile), "..", "..", "scripts", "bench", "fixtures", "models", "codex-debug-models.trimmed.json"))), &catalog); e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	h := &harness{t: t, root: t.TempDir()}
	t.Chdir(h.root)
	var e error
	h.cfg, e = config.Load(h.root)
	if e != nil {
		t.Fatal(e)
	}
	h.s, e = store.Open(h.root)
	if e != nil {
		t.Fatal(e)
	}
	h.reg, e = session.Open(h.s.Dir)
	if e != nil {
		t.Fatal(e)
	}
	h.path = filepath.Join(h.root, "task.md")
	h.task("- a.go")
	put(t, filepath.Join(h.root, "a.go"), strings.Repeat("line\n", 50))
	h.trust(true)
	oldRun, oldResolve := runWorker, resolveModel
	t.Cleanup(func() { runWorker = oldRun; resolveModel = oldResolve })
	runWorker = func(_ context.Context, s runner.Spec) (runner.Result, error) {
		i := len(h.calls)
		h.calls = append(h.calls, s)
		if h.change != "" {
			put(t, filepath.Join(h.root, h.change), "changed\n")
		}
		r := runner.Result{Done: true, Summary: "Completed.", Files: []string{"a.go"}, Needs: []string{}, SessionID: "worker-id", Usage: runner.Usage{Input: 12, Output: 3}}
		var e error
		if i < len(h.results) {
			r = h.results[i]
		}
		if i < len(h.errs) {
			e = h.errs[i]
		}
		return r, e
	}
	resolveModel = func(ctx context.Context, cfg *config.Config, dir string, target config.Target) (string, error) {
		return (models.Resolver{List: func(context.Context) ([]models.Entry, string, error) {
			return catalog.Models, "fixture", nil
		}}).Resolve(ctx, cfg, target)
	}
	return h
}
func (h *harness) task(refs string) {
	put(h.t, h.path, "# Task: example\n\n## Goal\nDo work.\n\n## Read\n"+refs+"\n\n## Write\n- a.go\n\n## Limits\nStay focused.\n\n## Check\ngo test ./...\n")
}
func (h *harness) trust(trusted bool) {
	s := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", h.root)
	if trusted {
		s += fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(h.root, ".codex", "hooks.json")+":PreToolUse:0:0")
	}
	put(h.t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), s)
}
func (h *harness) lead(agent string, m mode.Mode, effort string) {
	h.t.Helper()
	model := "opus"
	if agent == "codex" {
		model = "sol"
	}
	if e := h.reg.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: agent, Model: model, Effort: effort}, Mode: m}); e != nil {
		h.t.Fatal(e)
	}
	h.t.Setenv("DUOLOG_LEAD", "lead-123456")
}
func (h *harness) run(role string) (Outcome, error) {
	h.t.Helper()
	return Run(context.Background(), h.cfg, h.s, role, h.path, "topic")
}
func (h *harness) limited() {
	h.results = []runner.Result{{QuotaEnded: true}}
	h.errs = []error{errors.New("quota exceeded")}
}
func (h *harness) requireCalls(n int) {
	h.t.Helper()
	if len(h.calls) != n {
		h.t.Fatalf("calls=%d want %d", len(h.calls), n)
	}
}
func (h *harness) unavailable(agent string) {
	h.t.Helper()
	if e := h.reg.MarkUnavailable(agent, time.Time{}); e != nil {
		h.t.Fatal(e)
	}
}
func TestInlineNeverReachesBusOrOutput(t *testing.T) {
	h := newHarness(t)
	put(t, filepath.Join(h.root, "a.go"), "PRIVATE_FILE_CONTENT\n")
	out, e := h.run("implement")
	if e != nil {
		t.Fatal(e)
	}
	h.requireCalls(1)
	if !strings.Contains(string(h.calls[0].Stdin), "PRIVATE_FILE_CONTENT") {
		t.Fatal("missing stdin")
	}
	if strings.Contains(out.Line, "PRIVATE_FILE_CONTENT") || strings.Contains(read(t, filepath.Join(h.s.Dir, "bus.ndjson")), "PRIVATE_FILE_CONTENT") {
		t.Fatal("content leaked")
	}
}
func TestWriteRoleRefusedWithoutHookTrust(t *testing.T) {
	h := newHarness(t)
	h.trust(false)
	o, e := h.run("implement")
	h.requireCalls(0)
	if e == nil || !strings.Contains(o.Line, "/hooks") {
		t.Fatal(o, e)
	}
}
func TestReadRoleWarnsWithoutHookTrust(t *testing.T) {
	h := newHarness(t)
	h.trust(false)
	o, e := h.run("review_intermediate")
	h.requireCalls(1)
	if e != nil || !strings.Contains(o.Line, "rules are off") {
		t.Fatal(o, e)
	}
}
func TestFolderTrustReportedMissing(t *testing.T) {
	h := newHarness(t)
	put(t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), "")
	o, e := h.run("review_intermediate")
	if e != nil || !strings.Contains(o.Line, "folder trust missing") {
		t.Fatal(o, e)
	}
}
func TestFreshRunsAreNotRegistered(t *testing.T) {
	h := newHarness(t)
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	if x, ok := h.reg.Lookup("", "codex", "implement", "topic"); ok {
		t.Fatal(x)
	}
	if !strings.Contains(strings.Join(runner.Argv(h.calls[0]), " "), "--ephemeral") {
		t.Fatal("fresh persisted")
	}
}
func TestResumeReusesThread(t *testing.T) {
	h := newHarness(t)
	if _, e := h.run("plan"); e != nil {
		t.Fatal(e)
	}
	if _, e := h.run("plan"); e != nil {
		t.Fatal(e)
	}
	h.requireCalls(2)
	if h.calls[1].ThreadID != "worker-id" {
		t.Fatal(h.calls)
	}
	if strings.Contains(strings.Join(runner.Argv(h.calls[0]), " "), "--no-session-persistence") {
		t.Fatal("initial resumable run cannot persist")
	}
}
func TestResumeOverBudgetStartsFresh(t *testing.T) {
	h := newHarness(t)
	h.cfg.Sessions.ResumeMaxInputTokens = 10
	for i := 0; i < 2; i++ {
		if _, e := h.run("plan"); e != nil {
			t.Fatal(e)
		}
	}
	if h.calls[1].ThreadID != "" {
		t.Fatal(h.calls[1])
	}
}
func TestLimitStartsNoNewSession(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.limited()
	o, e := h.run("implement")
	h.requireCalls(1)
	if e != nil || o.RunHere == nil {
		t.Fatal(o, e)
	}
}
func TestLaterDelegateSkipsLimitedAgent(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.unavailable("codex")
	o, e := h.run("implement")
	h.requireCalls(0)
	if e != nil || o.RunHere == nil {
		t.Fatal(o, e)
	}
}
func TestSmallTaskRunsInSelf(t *testing.T) {
	h := newHarness(t)
	p, e := Place(h.cfg, h.path, router.Route{Target: config.Target{Model: "sonnet", Effort: "medium"}})
	if e != nil || p.Where != "self" {
		t.Fatal(p, e)
	}
}
func TestLargeTaskRunsInSubagent(t *testing.T) {
	h := newHarness(t)
	for _, refs := range []string{"- a.go\n- b.go\n- c.go\n- d.go", "- big.go"} {
		for _, name := range []string{"b.go", "c.go", "d.go", "big.go"} {
			put(t, filepath.Join(h.root, name), strings.Repeat("x\n", 201))
		}
		h.task(refs)
		p, e := Place(h.cfg, h.path, router.Route{Target: config.Target{Model: "sonnet", Effort: "medium"}})
		if e != nil || p.Where != "subagent" || p.Model != "sonnet" || p.Effort != "medium" {
			t.Fatal(p, e)
		}
	}
}
func TestRangeCountsOnlyItsLines(t *testing.T) {
	h := newHarness(t)
	put(t, filepath.Join(h.root, "a.go"), strings.Repeat("x\n", 500))
	h.task("- a.go:12-40")
	h.cfg.Limits.SelfMaxReadLines = 29
	p, e := Place(h.cfg, h.path, router.Route{})
	if e != nil || p.Where != "self" {
		t.Fatal(p, e)
	}
	h.cfg.Limits.SelfMaxReadLines = 28
	p, e = Place(h.cfg, h.path, router.Route{})
	if e != nil || p.Where != "subagent" {
		t.Fatal(p, e)
	}
}
func TestMissingReadFileCountsAsLarge(t *testing.T) {
	h := newHarness(t)
	h.task("- missing.go")
	p, e := Place(h.cfg, h.path, router.Route{})
	if e != nil || p.Where != "subagent" {
		t.Fatal(p, e)
	}
}
func TestRunHereLineIsExact(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.unavailable("codex")
	h.task("- missing.go")
	o, e := h.run("implement")
	if e != nil || o.Line != "run here: subagent model=sonnet effort=medium task="+h.path {
		t.Fatal(o, e)
	}
}
func TestLeadToldAboutMove(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.limited()
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	text := read(t, filepath.Join(h.s.Dir, "bus.ndjson"))
	for _, s := range []string{"implement", "codex", "claude", "sonnet", "medium"} {
		if !strings.Contains(text, s) {
			t.Fatal(text)
		}
	}
	if strings.Count(text, "moved") != 1 {
		t.Fatal(text)
	}
}
func TestPartialWorkNoted(t *testing.T) {
	h := newHarness(t)
	h.limited()
	h.change = "partial.go"
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	h.requireCalls(2)
	p := filepath.Join(h.calls[1].TaskDir, "task.md")
	s := read(t, p)
	if !strings.Contains(s, "partial.go") || !strings.Contains(strings.Split(s, "## Limits")[1], "partial.go") {
		t.Fatal(s)
	}
	if e := task.Validate(p); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(h.calls[1].Stdin), "partial.go") {
		t.Fatal("note absent in next stdin")
	}
}
func TestNoNoteWithoutChanges(t *testing.T) {
	h := newHarness(t)
	h.limited()
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(read(t, filepath.Join(h.calls[1].TaskDir, "task.md")), "Partial work") {
		t.Fatal("spurious note")
	}
}
func TestLimitOnBothAgents(t *testing.T) {
	h := newHarness(t)
	h.unavailable("codex")
	h.unavailable("claude")
	o, e := h.run("implement")
	h.requireCalls(0)
	if e == nil || o.Line == "" {
		t.Fatal(o, e)
	}
	entries, e := os.ReadDir(filepath.Join(h.s.Dir, "tasks"))
	if e != nil || len(entries) != 1 {
		t.Fatal(entries, e)
	}
}
func TestMissingBinaryFallsBack(t *testing.T) {
	h := newHarness(t)
	h.errs = []error{fmt.Errorf("start: %w", exec.ErrNotFound)}
	o, e := h.run("implement")
	h.requireCalls(2)
	if e != nil || !o.Result.Done {
		t.Fatal(o, e)
	}
}
func TestInterruptedRunNoFallback(t *testing.T) {
	h := newHarness(t)
	h.results = []runner.Result{{Interrupted: true}}
	h.errs = []error{context.Canceled}
	o, e := h.run("implement")
	h.requireCalls(1)
	if e == nil || !strings.Contains(o.Line, "interrupted") {
		t.Fatal(o, e)
	}
}
func TestNoRetryWhenFallbackFails(t *testing.T) {
	h := newHarness(t)
	h.limited()
	h.errs = append(h.errs, errors.New("fallback exploded"))
	o, e := h.run("implement")
	h.requireCalls(2)
	if e == nil || !strings.Contains(e.Error(), "codex") || !strings.Contains(e.Error(), "claude") || o.Line == "" {
		t.Fatal(o, e)
	}
}
func TestRefusesNestedDelegation(t *testing.T) {
	h := newHarness(t)
	t.Setenv("DUOLOG_DEPTH", "1")
	o, e := h.run("implement")
	h.requireCalls(0)
	if e == nil || o.Line == "" {
		t.Fatal(o, e)
	}
}
func TestChildGetsDepthEnv(t *testing.T) {
	h := newHarness(t)
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(h.calls[0].Env, "\n"), "DUOLOG_DEPTH=1") {
		t.Fatal(h.calls)
	}
}
func TestRunnerGetsConcreteCodexModel(t *testing.T) {
	h := newHarness(t)
	if _, e := h.run("review_intermediate"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(runner.Argv(h.calls[0]), " "), "-m gpt-6.1-terra") {
		t.Fatal(h.calls)
	}
}
func TestRunnerGetsClaudeAlias(t *testing.T) {
	h := newHarness(t)
	if _, e := h.run("review_final"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(runner.Argv(h.calls[0]), " "), "--model opus") {
		t.Fatal(h.calls)
	}
}
func TestRunnerGetsLeadMode(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	if h.calls[0].Mode != mode.Edits {
		t.Fatal(h.calls)
	}
	if _, e := h.run("review_intermediate"); e != nil {
		t.Fatal(e)
	}
	if h.calls[1].Mode != mode.Plan {
		t.Fatal(h.calls)
	}
}
func TestPlanModeRefusesWriteRole(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Plan, "high")
	o, e := h.run("implement")
	h.requireCalls(0)
	if e == nil || !strings.Contains(o.Line, "plan") {
		t.Fatal(o, e)
	}
}
func TestPlanModeAllowsReview(t *testing.T) {
	h := newHarness(t)
	h.lead("codex", mode.Plan, "high")
	if _, e := h.run("review_final"); e != nil {
		t.Fatal(e)
	}
	h.requireCalls(1)
	if h.calls[0].Mode != mode.Plan {
		t.Fatal(h.calls)
	}
}
func TestChildGetsLeadEnv(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(h.calls[0].Env, "\n"), "DUOLOG_LEAD=lead-123456") {
		t.Fatal(h.calls)
	}
}
func TestRunnerGetsLeadCappedEffort(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "medium")
	if _, e := h.run("implement_complex"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(runner.Argv(h.calls[0]), " "), `model_reasoning_effort="medium"`) {
		t.Fatal(h.calls)
	}
}
func TestUsageRecordsCappedEffort(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "medium")
	if _, e := h.run("implement_complex"); e != nil {
		t.Fatal(e)
	}
	var u map[string]any
	if e := json.Unmarshal([]byte(strings.TrimSpace(read(t, filepath.Join(h.s.Dir, "usage.ndjson")))), &u); e != nil {
		t.Fatal(e)
	}
	if u["effort"] != "medium" || u["effort_capped"] != true {
		t.Fatal(u)
	}
}
func TestEitherAgentCanLead(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			h := newHarness(t)
			h.lead(agent, mode.Edits, "high")
			role := "implement"
			if agent == "codex" {
				role = "review_final"
			}
			if _, e := h.run(role); e != nil {
				t.Fatal(e)
			}
			h.requireCalls(1)
			if h.calls[0].Route.Agent == agent {
				t.Fatal(h.calls)
			}
		})
	}
}
func TestSelfRouteStartsNothing(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	o, e := h.run("plan")
	h.requireCalls(0)
	if e != nil || o.Line != "run here: self task="+h.path {
		t.Fatal(o, e)
	}
}
func TestUsageAppended(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 2; i++ {
		if _, e := h.run("implement"); e != nil {
			t.Fatal(e)
		}
	}
	text := read(t, filepath.Join(h.s.Dir, "usage.ndjson"))
	if strings.Count(text, "\n") != 2 {
		t.Fatal(text)
	}
	for _, s := range []string{`"role":"implement"`, `"model":"gpt-6.1-sol"`, `"input":12`, `"output":3`} {
		if !strings.Contains(text, s) {
			t.Fatal(text)
		}
	}
}
func TestBusGetsTaskAndAnswerLines(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	o, e := h.run("implement")
	if e != nil {
		t.Fatal(e)
	}
	text := read(t, filepath.Join(h.s.Dir, "bus.ndjson"))
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 {
		t.Fatal(text)
	}
	for i, line := range lines {
		var m bus.Message
		if e := json.Unmarshal([]byte(line), &m); e != nil {
			t.Fatal(e)
		}
		if i == 0 && (m.K != "task" || m.From != "claude/lead.lead-1") {
			t.Fatal(m)
		}
		if i == 1 && (m.K != "answer" || m.To != "claude/lead.lead-1" || m.S != o.Line) {
			t.Fatal(m)
		}
	}
	for _, s := range []string{"done=true", "Completed.", "a.go"} {
		if !strings.Contains(o.Line, s) {
			t.Fatal(o.Line)
		}
	}
}
func TestFakeBinaryReceivesInlineAndEnv(t *testing.T) {
	h := newHarness(t)
	h.lead("codex", mode.Edits, "medium")
	fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"session_id":"fake-worker","usage":{"input_tokens":17,"output_tokens":4},"structured_output":{"done":true,"summary":"Finished.","files":["a.go"],"needs":[]}}`})
	runWorker = runner.Run
	o, e := h.run("review_final")
	if e != nil || !o.Result.Done {
		t.Fatal(o, e)
	}
	if !strings.Contains(read(t, fake+".stdin"), "line\n") || !strings.Contains(read(t, fake+".env.json"), "DUOLOG_DEPTH=1") || !strings.Contains(read(t, fake+".args.json"), "medium") {
		t.Fatal("fake input mismatch")
	}
}

func TestMissingAnswerDoesNotFallback(t *testing.T) {
	h := newHarness(t)
	h.errs = []error{&os.PathError{Op: "open", Path: "answer.json", Err: os.ErrNotExist}}
	if _, e := h.run("implement"); e == nil {
		t.Fatal("missing answer accepted")
	}
	h.requireCalls(1)
}
func TestResumeClosesIdleThread(t *testing.T) {
	h := newHarness(t)
	if e := h.reg.Record(session.Entry{ID: "old", Agent: "claude", Role: "plan", Thread: "topic", ThreadID: "expired", UpdatedAt: time.Now().Add(-time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.run("plan"); e != nil {
		t.Fatal(e)
	}
	if h.calls[0].ThreadID != "" {
		t.Fatal("idle worker reused")
	}
}
func TestResumeLimitsLiveWorkers(t *testing.T) {
	h := newHarness(t)
	if e := h.reg.Record(session.Entry{ID: "old", Agent: "claude", Role: "plan", Thread: "other", ThreadID: "old-worker"}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.run("plan"); e != nil {
		t.Fatal(e)
	}
	if _, ok := h.reg.Lookup("", "claude", "plan", "other"); ok {
		t.Fatal("live worker cap ignored")
	}
}
func TestCodexResumeFirstRunPersists(t *testing.T) {
	h := newHarness(t)
	role := h.cfg.Roles["review_intermediate"]
	role.Session = "resume"
	h.cfg.Roles["review_intermediate"] = role
	if _, e := h.run("review_intermediate"); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(strings.Join(runner.Argv(h.calls[0]), " "), "--ephemeral") {
		t.Fatal("initial resumable Codex run is ephemeral")
	}
}
func TestNoLeadUsesAskWithoutEffortCap(t *testing.T) {
	h := newHarness(t)
	t.Setenv("DUOLOG_LEAD", "unregistered")
	if _, e := h.run("implement_complex"); e != nil {
		t.Fatal(e)
	}
	if h.calls[0].Mode != mode.Ask || h.calls[0].Route.Effort != "high" || h.calls[0].Route.EffortCapped {
		t.Fatal(h.calls[0])
	}
}
func TestFallbackFailureNamesBothAgents(t *testing.T) {
	h := newHarness(t)
	h.limited()
	resolveModel = func(_ context.Context, _ *config.Config, _ string, target config.Target) (string, error) {
		if target.Agent == "claude" {
			return "", errors.New("bad catalog")
		}
		return "gpt-6.1-sol", nil
	}
	_, e := h.run("implement")
	h.requireCalls(1)
	if e == nil || !strings.Contains(e.Error(), "codex") || !strings.Contains(e.Error(), "claude") {
		t.Fatal(e)
	}
}

func TestResumeWithoutRepeatedSessionIDStillTracksUsage(t *testing.T) {
	h := newHarness(t)
	h.results = []runner.Result{{Done: true, SessionID: "worker-id", Usage: runner.Usage{Input: 9}}, {Done: true, Usage: runner.Usage{Input: 11}}}
	for i := 0; i < 2; i++ {
		if _, e := h.run("plan"); e != nil {
			t.Fatal(e)
		}
	}
	entry, ok := h.reg.Lookup("", "claude", "plan", "topic")
	if !ok || entry.Input != 20 || entry.ThreadID != "worker-id" {
		t.Fatal(entry, ok)
	}
}
func TestPartialWorkWithCRLFTask(t *testing.T) {
	h := newHarness(t)
	put(t, h.path, strings.ReplaceAll(read(t, h.path), "\n", "\r\n"))
	h.limited()
	h.change = "partial.go"
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	h.requireCalls(2)
	if !strings.Contains(string(h.calls[1].Stdin), "Partial work from codex: partial.go") {
		t.Fatal("missing partial-work note")
	}
}

func TestPartialWorkPlacementUsesAnnotatedCopy(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.limited()
	h.change = "partial.go"
	o, e := h.run("implement")
	h.requireCalls(1)
	if e != nil || o.RunHere == nil || o.RunHere.Task == h.path {
		t.Fatal(o, e)
	}
	if !strings.Contains(read(t, o.RunHere.Task), "partial.go") {
		t.Fatal("lead did not receive partial-work note")
	}
}
