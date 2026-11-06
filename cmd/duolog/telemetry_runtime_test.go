package main

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/usage"
)

// Tests in this package never write the person's telemetry: collection is
// off unless a test turns it on in a temporary home with telemetryHome.
func init() {
	if err := os.Setenv("DUOLOG_TELEMETRY", "off"); err != nil {
		panic(err)
	}
}

// telemetryHome turns collection on with every telemetry root inside a
// temporary home, and returns the telemetry root.
func telemetryHome(t *testing.T) string {
	t.Helper()
	isolatedRouteConfig(t)
	home := os.Getenv("HOME")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("DUOLOG_TELEMETRY", "")
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	root, err := telemetry.ResolveRoot(telemetryLocation())
	if err != nil || !strings.HasPrefix(root, home) {
		t.Fatalf("telemetry root %s outside the temporary home: %v", root, err)
	}
	return root
}

// delegationProject is a project with a registered lead and a fake worker
// agent; run delegates once and returns the printed line and the error.
type delegationProject struct {
	t        *testing.T
	root     string
	taskPath string
	fake     string
	reg      *session.Registry
}

func newDelegationProject(t *testing.T, dir string, scenario testbin.Scenario) *delegationProject {
	t.Helper()
	root := filepath.Join(t.TempDir(), dir)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	p := &delegationProject{t: t, root: root, taskPath: filepath.Join(root, "task.md")}
	for path, s := range map[string]string{p.taskPath: "# Task: command\n\n## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\nNone.\n\n## Limits\nNo changes.\n\n## Check\nReview.\n", filepath.Join(root, "a.go"): "package a\n"} {
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The login check answers at once; only worker runs take the scenario.
	p.fake = testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{"auth status": {Stdout: `{"loggedIn":true}`}, "-p": scenario}})
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.reg, err = session.Open(s.Dir); err != nil {
		t.Fatal(err)
	}
	p.lead("codex", "sol", mode.Plan)
	return p
}

func (p *delegationProject) lead(agent, model string, m mode.Mode) {
	p.t.Helper()
	if err := p.reg.RegisterLead(session.Lead{ID: "registered-lead", Target: config.Target{Agent: agent, Model: model, Effort: "medium"}, Mode: m}); err != nil {
		p.t.Fatal(err)
	}
}

func (p *delegationProject) run(role string) (string, error) {
	p.t.Helper()
	return executeBusCommand(p.t, "delegate", role, "--task", p.taskPath, "--lead", "registered-lead", "--thread", "topic")
}

// runs reads this project's run events.
func runs(t *testing.T, root string) []telemetry.Run {
	t.Helper()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := telemetry.Open(telemetryOptions(cfg, root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Read(telemetry.Query{})
	if err != nil {
		t.Fatal(err)
	}
	var out []telemetry.Run
	for _, e := range events {
		if r, ok := e.(telemetry.Run); ok {
			out = append(out, r)
		}
	}
	return out
}

func reviewed(summary string) testbin.Scenario {
	return testbin.Scenario{Stdout: `{"session_id":"vendor-session","structured_output":{"done":true,"summary":"` + summary + `","files":["a.go"],"needs":[]},"usage":{"input_tokens":100,"cache_read_input_tokens":40,"output_tokens":10}}`}
}

// No vendor text reaches telemetry: the answer, stderr, the task path, the
// vendor session id and the error stay out, and the run id is duolog's own.
func TestAttemptNoVendorText(t *testing.T) {
	telemetryRoot := telemetryHome(t)
	const sentinel = "sentinelvendortext"
	ok := testbin.Scenario{Stdout: `{"session_id":"` + sentinel + `-session","structured_output":{"done":true,"summary":"` + sentinel + ` answer","files":["` + sentinel + `.go"],"needs":[]},"usage":{"input_tokens":10,"output_tokens":2}}`, Stderr: sentinel + " stderr\n"}
	failed := testbin.Scenario{Exit: 1, Stderr: sentinel + " error at /" + sentinel + "/path\n"}
	p := newDelegationProject(t, sentinel+"-project", testbin.Scenario{Sequence: []testbin.Scenario{ok, failed}})
	if out, err := p.run("review_final"); err != nil || !strings.Contains(out, "done=true") {
		t.Fatal(out, err)
	}
	if out, err := p.run("review_final"); err == nil {
		t.Fatal("failed worker succeeded:", out)
	}
	cfg, err := config.Load(p.root)
	if err != nil {
		t.Fatal(err)
	}
	s := openTelemetry(cfg, p.root)
	if s == nil {
		t.Fatal("telemetry did not open")
	}
	observeAttempt(s, cfg)(delegate.Attempt{Number: 1, Role: sentinel, Class: sentinel, Agent: sentinel, Family: sentinel, Effort: sentinel, Placement: sentinel, Outcome: sentinel, ErrorCode: sentinel, Reason: sentinel})
	s.Close()

	got := runs(t, p.root)
	if len(got) != 3 {
		t.Fatalf("run events=%d want 3: %+v", len(got), got)
	}
	if got[0].Outcome != "done" || got[0].Err != "none" || got[1].Outcome != "failed" || got[1].Err != "exit" || got[2].Role != "other" || got[2].Family != "other" || got[2].Err != "other" {
		t.Fatalf("%+v", got)
	}
	ids := map[string]bool{}
	hexID := regexp.MustCompile(`^"[0-9a-f]{32}"$`)
	for _, r := range got {
		id, err := json.Marshal(r.Run)
		if err != nil || !hexID.Match(id) {
			t.Fatalf("run id %s: %v", id, err)
		}
		ids[string(id)] = true
	}
	if len(ids) != 3 {
		t.Fatalf("run ids repeat: %v", ids)
	}
	err = filepath.Walk(telemetryRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(path), sentinel) || strings.Contains(strings.ToLower(string(data)), sentinel) || strings.Contains(string(data), "vendor-session") {
			t.Errorf("vendor text in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Cost comes from usage.Cost, which charges cached input once, as
// round(USD*1e6) when the usage is known, the family priced and the result
// finite and in range.
func TestAttemptUsageAndCost(t *testing.T) {
	isolatedRouteConfig(t)
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opus := cfg.Models["opus"]
	priced := func(in, cached, out int64) int64 {
		usd, ok := usage.Cost(cfg, "claude", "opus", usage.Tokens{Input: in, Cached: cached, Output: out})
		if !ok {
			t.Fatal("opus unpriced")
		}
		return int64(math.Round(usd * 1e6))
	}
	if want := int64(math.Round((600*opus.Input + 400*opus.Cached + 100*opus.Output))); priced(1000, 400, 100) != want {
		t.Fatalf("cached charged twice: %d want %d", priced(1000, 400, 100), want)
	}
	base := delegate.Attempt{Number: 1, Role: "implement", Agent: "claude", Family: "opus", Effort: "high", Placement: "worker", Outcome: "done", ErrorCode: "none", Reason: "none", Duration: 1500 * time.Millisecond}
	with := func(change func(*delegate.Attempt)) delegate.Attempt {
		a := base
		change(&a)
		return a
	}
	huge := *cfg
	huge.Models = map[string]config.Model{"opus": {Agent: "claude", Input: math.MaxFloat64}}
	nan := *cfg
	nan.Models = map[string]config.Model{"opus": {Agent: "claude", Input: math.NaN()}}
	cases := []struct {
		name                 string
		cfg                  *config.Config
		attempt              delegate.Attempt
		in, cached, out, usd int64
		known, isPriced      bool
		family               string
	}{
		{"cached once", cfg, with(func(a *delegate.Attempt) {
			a.Usage, a.UsageKnown = runner.Usage{Input: 1000, Cached: 400, Output: 100}, true
		}), 1000, 400, 100, priced(1000, 400, 100), true, true, "opus"},
		{"known zero", cfg, with(func(a *delegate.Attempt) { a.UsageKnown = true }), 0, 0, 0, 0, true, true, "opus"},
		{"unknown", cfg, with(func(a *delegate.Attempt) { a.Usage = runner.Usage{Input: 1000, Output: 100} }), 0, 0, 0, 0, false, false, "opus"},
		{"unpriced", cfg, with(func(a *delegate.Attempt) {
			a.Family, a.Usage, a.UsageKnown = "sol", runner.Usage{Input: 1000, Output: 100}, true
		}), 1000, 0, 100, 0, true, false, "sol"},
		{"codex family", cfg, with(func(a *delegate.Attempt) {
			a.Agent, a.Family, a.Usage, a.UsageKnown = "codex", "sol", runner.Usage{Input: 1000, Cached: 400, Output: 100}, true
		}), 1000, 400, 100, func() int64 {
			usd, _ := usage.Cost(cfg, "codex", "sol", usage.Tokens{Input: 1000, Cached: 400, Output: 100})
			return int64(math.Round(usd * 1e6))
		}(), true, true, "sol"},
		// 300 five-minute writes at 1.25x and 200 one-hour writes at 2x input.
		{"cache writes", cfg, with(func(a *delegate.Attempt) {
			a.Usage, a.UsageKnown = runner.Usage{Input: 1000, Cached: 400, Write: 500, WriteHour: 200, Output: 100}, true
		}), 1000, 400, 100, int64(math.Round(100*opus.Input + 400*opus.Cached + 300*1.25*opus.Input + 200*2*opus.Input + 100*opus.Output)), true, true, "opus"},
		{"writes over input", cfg, with(func(a *delegate.Attempt) {
			a.Usage, a.UsageKnown = runner.Usage{Input: 1000, Cached: 400, Write: 900, WriteHour: 2000, Output: 100}, true
		}), 1000, 400, 100, int64(math.Round(400*opus.Cached + 600*2*opus.Input + 100*opus.Output)), true, true, "opus"},
		{"cached over input", cfg, with(func(a *delegate.Attempt) {
			a.Usage, a.UsageKnown = runner.Usage{Input: 10, Cached: 50, Output: -3}, true
		}), 10, 10, 0, priced(10, 10, 0), true, true, "opus"},
		{"out of range", &huge, with(func(a *delegate.Attempt) {
			a.Usage, a.UsageKnown = runner.Usage{Input: 1 << 40}, true
		}), 1 << 40, 0, 0, 0, true, false, "opus"},
		{"not finite", &nan, with(func(a *delegate.Attempt) {
			a.Usage, a.UsageKnown = runner.Usage{Input: 1000}, true
		}), 1000, 0, 0, 0, true, false, "opus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runEvent(c.cfg, telemetry.ID{}, c.attempt)
			if r.In != c.in || r.Cached != c.cached || r.Out != c.out || r.USDmicro != c.usd || r.UsageKnown != c.known || r.Priced != c.isPriced || r.Family != c.family || r.Secs != 2 || r.Attempt != 1 {
				t.Fatalf("%+v", r)
			}
			if _, err := telemetry.Encode(r, telemetry.Names{Roles: []string{"implement"}}, time.Now()); err != nil {
				t.Fatalf("event refused: %v", err)
			}
		})
	}
}

// A sink that is off, capped or failing changes nothing in the delegation:
// the same line, the same error and the same worker command.
func TestAttemptNoopSink(t *testing.T) {
	telemetryRoot := telemetryHome(t)
	p := newDelegationProject(t, "project", reviewed("Reviewed."))
	type result struct{ line, err, args string }
	run := func() result {
		t.Helper()
		out, err := p.run("review_final")
		args, readErr := os.ReadFile(p.fake + ".args.json")
		if readErr != nil {
			t.Fatal(readErr)
		}
		r := result{line: out, args: string(args)}
		if err != nil {
			r.err = err.Error()
		}
		return r
	}
	want := run()
	if want.err != "" || !strings.Contains(want.line, "done=true") || len(runs(t, p.root)) != 1 {
		t.Fatalf("%+v", want)
	}

	t.Run("off", func(t *testing.T) {
		t.Setenv("DUOLOG_TELEMETRY", "off")
		if got := run(); got != want {
			t.Fatalf("%+v want %+v", got, want)
		}
	})
	if n := len(runs(t, p.root)); n != 1 {
		t.Fatalf("off wrote %d events", n-1)
	}
	t.Run("capped", func(t *testing.T) {
		segments, err := filepath.Glob(filepath.Join(telemetryRoot, "v1", "*", "*.ndjson"))
		if err != nil || len(segments) != 1 {
			t.Fatal(segments, err)
		}
		f, err := os.OpenFile(segments[0], os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(strings.Repeat(strings.Repeat("x", 999)+"\n", 2000))
		if err = errors.Join(err, f.Close()); err != nil {
			t.Fatal(err)
		}
		before := (*telemetry.Store)(nil).Health().Dropped
		if got := run(); got != want {
			t.Fatalf("%+v want %+v", got, want)
		}
		if (*telemetry.Store)(nil).Health().Dropped != before+1 {
			t.Fatal("capped event not counted")
		}
	})
	t.Run("failed", func(t *testing.T) {
		cfg, err := config.Load(p.root)
		if err != nil {
			t.Fatal(err)
		}
		s := openTelemetry(cfg, p.root)
		if s == nil {
			t.Fatal("telemetry did not open")
		}
		s.Close()
		before := s.Health().Failures
		observeAttempt(s, cfg)(delegate.Attempt{Number: 1, Role: "review_final", Placement: "worker", Outcome: "done"})
		if s.Health().Failures != before+1 {
			t.Fatal("failed append not counted")
		}
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink needs a privilege on Windows")
		}
		// A root the store refuses to use: open fails and the delegation runs as before.
		if err := os.Rename(telemetryRoot, telemetryRoot+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(telemetryRoot+".moved", telemetryRoot); err != nil {
			t.Fatal(err)
		}
		if got := run(); got != want {
			t.Fatalf("%+v want %+v", got, want)
		}
	})
}

// Placement follows where the attempt ran: a worker, a peer, or the lead
// itself once it finished a run here task. A run here line alone is no run.
func TestPlacementObservations(t *testing.T) {
	telemetryHome(t)
	p := newDelegationProject(t, "project", reviewed("Reviewed."))
	p.lead("claude", "opus", mode.Edits)
	if err := p.reg.MarkUnavailable("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	out, err := p.run("implement")
	if err != nil || !strings.HasPrefix(out, "run here:") {
		t.Fatal(out, err)
	}
	if got := runs(t, p.root); len(got) != 0 {
		t.Fatalf("run here recorded a run: %+v", got)
	}
	cfg, err := config.Load(p.root)
	if err != nil {
		t.Fatal(err)
	}
	s := openTelemetry(cfg, p.root)
	if s == nil {
		t.Fatal("telemetry did not open")
	}
	observe := observeAttempt(s, cfg)
	self := router.Route{Target: config.Target{Agent: "claude", Model: "opus", Effort: "high"}, Self: true}
	inLead := router.Route{Target: config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}, InLead: true, IsFallback: true}
	delegate.ObserveHere(observe, "implement", "", self, "done", time.Minute)
	delegate.ObserveHere(observe, "implement", "", inLead, "failed", time.Minute)
	observe(delegate.Attempt{Number: 1, Role: "implement", Agent: "codex", Family: "sol", Effort: "medium", Placement: "peer", Outcome: "done", ErrorCode: "none", Reason: "none", Duration: time.Minute})
	s.Close()
	got := runs(t, p.root)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	want := []struct{ placement, outcome, family string }{{"self", "done", "opus"}, {"inlead", "failed", "sonnet"}, {"peer", "done", "sol"}}
	for i, w := range want {
		r := got[i]
		if r.Placement != w.placement || r.Outcome != w.outcome || r.Family != w.family || r.Secs != 60 || r.Attempt != 1 || r.UsageKnown {
			t.Fatalf("event %d: %+v want %+v", i, r, w)
		}
	}
	if !got[1].Moved || got[1].Reason != "availability" || got[0].Moved {
		t.Fatalf("%+v", got)
	}
}
