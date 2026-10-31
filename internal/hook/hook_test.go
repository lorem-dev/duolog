package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func defaults(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("../../scripts/bench/fixtures", path))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func setup(t *testing.T, agent string) (Env, *session.Registry, *bus.Bus) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	root := t.TempDir()
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(reg.Dir, defaults(t).Wire)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "transcript.jsonl")
	name := "claude-assistant.trimmed.jsonl"
	if agent == "codex" {
		name = "codex-turn-context.trimmed.jsonl"
	}
	write(t, path, string(fixture(t, "transcripts/"+name)))
	var h map[string]any
	if err := json.Unmarshal(fixture(t, "hooks/"+agent+"-sessionstart-interactive.json"), &h); err != nil {
		t.Fatal(err)
	}
	h["transcript_path"] = path
	h["permission_mode"] = "default"
	input, _ := json.Marshal(h)
	return Env{Root: root, Agent: agent, Input: input, Environ: []string{"CLAUDE_CODE_ENTRYPOINT=cli"}}, reg, b
}
func change(t *testing.T, e Env, key string, v any) Env {
	t.Helper()
	var h map[string]any
	if err := json.Unmarshal(e.Input, &h); err != nil {
		t.Fatal(err)
	}
	h[key] = v
	e.Input, _ = json.Marshal(h)
	return e
}
func id(e Env) string {
	var h struct {
		ID string `json:"session_id"`
	}
	_ = json.Unmarshal(e.Input, &h)
	return h.ID
}
func transcript(e Env) string {
	var h struct {
		Path string `json:"transcript_path"`
	}
	_ = json.Unmarshal(e.Input, &h)
	return h.Path
}
func lead(t *testing.T, r *session.Registry, e Env) session.Lead {
	t.Helper()
	l, ok, err := r.Lead(id(e))
	if err != nil || !ok {
		t.Fatalf("missing lead: %v %v", ok, err)
	}
	return l
}
func silent(t *testing.T, o Output) {
	t.Helper()
	if len(o.Stdout) != 0 || len(o.Stderr) != 0 || o.Code != 0 {
		t.Fatalf("not silent: %+v", o)
	}
}
func content(t *testing.T, o Output, event string) string {
	t.Helper()
	if o.Code != 0 || len(o.Stderr) != 0 {
		t.Fatalf("bad output: %+v", o)
	}
	var h struct {
		Specific struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(o.Stdout, &h); err != nil {
		t.Fatal(err, string(o.Stdout))
	}
	if h.Specific.Event != event {
		t.Fatalf("event %s", h.Specific.Event)
	}
	return h.Specific.Context
}
func send(t *testing.T, b *bus.Bus, to, s string) {
	t.Helper()
	if _, err := b.Send(bus.Message{From: "codex/review", To: to, K: "done", S: s}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStartRegistersLead(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			e, r, _ := setup(t, agent)
			SessionStart(context.Background(), e)
			l := lead(t, r, e)
			if l.Agent != agent || l.Model == "" || l.Effort == "" || l.TranscriptPath != transcript(e) || l.PID != os.Getppid() {
				t.Fatalf("%+v", l)
			}
		})
	}
}
func TestWorkerNotRegisteredAsLead(t *testing.T) {
	e, r, _ := setup(t, "claude")
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	silent(t, SessionStart(context.Background(), e))
	if _, ok, _ := r.Lead(id(e)); ok {
		t.Fatal("worker registered")
	}
}
func TestSecondLeadKeepsFirst(t *testing.T) {
	e, r, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	other := change(t, e, "session_id", "second-123456")
	SessionStart(context.Background(), other)
	lead(t, r, e)
	lead(t, r, other)
}
func TestLeadWithoutModelInfo(t *testing.T) {
	e, r, _ := setup(t, "claude")
	e = change(t, e, "model", nil)
	write(t, transcript(e), "")
	SessionStart(context.Background(), e)
	l := lead(t, r, e)
	if l.Model != "" {
		t.Fatal(l)
	}
	route, err := router.Resolve(defaults(t), "review_final", nil, router.Options{Lead: &l.Target})
	if err != nil || route.Self {
		t.Fatal(route, err)
	}
}
func TestLeadStateFromTranscript(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			e, _, _ := setup(t, agent)
			s, err := session.ReadLeadState(agent, transcript(e))
			if err != nil {
				t.Fatal(err)
			}
			if agent == "claude" {
				if s.Model != "claude-opus-5-5" || s.Effort != "high" {
					t.Fatal(s)
				}
			} else if s.Model != "gpt-6.1-terra" || s.Effort != "medium" || s.Mode != mode.Ask || s.ApprovalPolicy != "on-request" {
				t.Fatal(s)
			}
		})
	}
}
func TestCodexNullEffortUsesDefault(t *testing.T) {
	e, _, _ := setup(t, "codex")
	testbin.Install(t, "codex", testbin.Scenario{Stdout: `{"models":[{"slug":"gpt-test","default_reasoning_level":"medium"}]}`})
	write(t, transcript(e), "{\"type\":\"turn_context\",\"payload\":{\"model\":\"gpt-test\",\"effort\":null}}\n")
	s, err := session.ReadLeadState("codex", transcript(e))
	if err != nil || s.Effort != "medium" {
		t.Fatal(s, err)
	}
}
func TestBadTranscriptIsUnknown(t *testing.T) {
	for _, data := range []string{"", "{bad}\n"} {
		e, _, _ := setup(t, "claude")
		write(t, transcript(e), data)
		s, err := session.ReadLeadState("claude", transcript(e))
		if err != nil || s.Model != "" || s.Effort != "" {
			t.Fatal(s, err)
		}
	}
	s, err := session.ReadLeadState("codex", filepath.Join(t.TempDir(), "missing"))
	if err != nil || s.Model != "" || s.Effort != "" {
		t.Fatal(s, err)
	}
}
func TestOnlyInteractiveRegisters(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		e, r, _ := setup(t, agent)
		if agent == "claude" {
			e.Environ = []string{"CLAUDE_CODE_ENTRYPOINT=sdk-cli"}
		} else {
			write(t, transcript(e), "{\"type\":\"session_meta\",\"payload\":{\"originator\":\"codex_exec\"}}\n")
		}
		silent(t, SessionStart(context.Background(), e))
		if _, ok, _ := r.Lead(id(e)); ok {
			t.Fatal(agent)
		}
	}
}
func TestOwnAgentSignalsOnly(t *testing.T) {
	e, r, _ := setup(t, "codex")
	e.Environ = []string{"CLAUDE_CODE_ENTRYPOINT=sdk-cli", "CLAUDE_EFFORT=ultra"}
	SessionStart(context.Background(), e)
	if l := lead(t, r, e); l.Effort != "medium" {
		t.Fatal(l)
	}
}
func TestDeadLeadProcessIsEnded(t *testing.T) {
	e, r, _ := setup(t, "codex")
	SessionStart(context.Background(), e)
	l := lead(t, r, e)
	l.PID = 1 << 30
	if err := r.RegisterLead(l); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.Lead(id(e)); err != nil || ok {
		t.Fatal(ok, err)
	}
}
func TestRewakeDoesNotRepeat(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	send(t, b, session.Address(lead(t, r, e)), "once")
	var stderr bytes.Buffer
	e.Stderr = &stderr
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if code := Wake(ctx, e); code != 2 || !strings.Contains(stderr.String(), "once") {
		t.Fatal(code, stderr.String())
	}
	e = change(t, e, "prompt", string(fixture(t, "hooks/claude-userpromptsubmit-rewake.json")))
	silent(t, Prompt(context.Background(), e))
}
func TestSessionEndMarksLeadGone(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		e, r, _ := setup(t, agent)
		SessionStart(context.Background(), e)
		var h map[string]any
		_ = json.Unmarshal(fixture(t, "hooks/"+agent+"-sessionend.json"), &h)
		h["session_id"] = id(e)
		e.Input, _ = json.Marshal(h)
		silent(t, SessionEnd(context.Background(), e))
		if _, ok, _ := r.Lead(id(e)); ok {
			t.Fatal(agent)
		}
	}
}
func TestWorkerSessionEndChangesNoLead(t *testing.T) {
	e, r, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	silent(t, SessionEnd(context.Background(), e))
	lead(t, r, e)
}
func TestLeadModeRecorded(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		e, r, _ := setup(t, agent)
		e = change(t, e, "permission_mode", "acceptEdits")
		SessionStart(context.Background(), e)
		want := mode.Edits
		if agent == "codex" {
			want = mode.Ask
		}
		if l := lead(t, r, e); l.Mode != want {
			t.Fatal(l)
		}
	}
}
func TestLeadModeFollowsChange(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		e, r, _ := setup(t, agent)
		SessionStart(context.Background(), e)
		if agent == "claude" {
			e = change(t, e, "permission_mode", "plan")
		} else {
			write(t, transcript(e), "{\"type\":\"turn_context\",\"payload\":{\"sandbox_policy\":{\"type\":\"read-only\"}}}\n")
		}
		silent(t, PostTool(context.Background(), e))
		if l := lead(t, r, e); l.Mode != mode.Plan {
			t.Fatal(l)
		}
	}
}
func TestLeadEffortUnknownStaysEmpty(t *testing.T) {
	e, r, _ := setup(t, "claude")
	e.Environ = append(e.Environ, "CLAUDE_EFFORT=low")
	write(t, transcript(e), "")
	SessionStart(context.Background(), e)
	l := lead(t, r, e)
	if l.Effort != "" {
		t.Fatal(l)
	}
	route, err := router.Resolve(defaults(t), "review_final", nil, router.Options{Lead: &l.Target})
	if err != nil || route.EffortCapped {
		t.Fatal(route, err)
	}
}
func TestInboxUsesLeadAddress(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	send(t, b, "claude/lead.other", "wrong")
	send(t, b, session.Address(lead(t, r, e)), "right")
	c := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	if !strings.Contains(c, "right") || strings.Contains(c, "wrong") {
		t.Fatal(c)
	}
}
func TestSilentWhenNoMessages(t *testing.T) {
	e, _, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	silent(t, Prompt(context.Background(), e))
	silent(t, PostTool(context.Background(), e))
}
func TestSilentWhenNotInitialized(t *testing.T) {
	e, _, _ := setup(t, "claude")
	e.Root = t.TempDir()
	for _, fn := range []func(context.Context, Env) Output{SessionStart, Prompt, PostTool, SessionEnd} {
		silent(t, fn(context.Background(), e))
	}
	if _, err := os.Stat(filepath.Join(e.Root, ".duolog")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestDeliversExactLines(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		e, r, _ := setup(t, agent)
		SessionStart(context.Background(), e)
		raw := fmt.Sprintf("{ \"n\":1, \"from\":\"other\", \"to\":%q, \"s\":\"<exact>  bytes\" }\n", session.Address(lead(t, r, e)))
		write(t, filepath.Join(r.Dir, "bus.ndjson"), raw)
		if c := content(t, PostTool(context.Background(), e), "PostToolUse"); c != raw {
			t.Fatalf("%q != %q", c, raw)
		}
	}
}
func TestDeliveryCapped(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	a := session.Address(lead(t, r, e))
	for i := 0; i < 100; i++ {
		send(t, b, a, fmt.Sprintf("message-%03d", i))
	}
	c := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	if strings.Count(c, `"n":`) != 10 || !strings.Contains(c, "message-009") || strings.Contains(c, "message-010") || !strings.Contains(c, "duolog inbox") {
		t.Fatal(c)
	}
	lines, err := b.Inbox(a)
	if err != nil || len(lines) != 90 {
		t.Fatal(len(lines), err)
	}
}
func TestSessionStartCarriesRoster(t *testing.T) {
	e, _, _ := setup(t, "claude")
	c := content(t, SessionStart(context.Background(), e), "SessionStart")
	if !strings.Contains(c, "duolog lead="+id(e)) || !strings.Contains(c, "review=") {
		t.Fatal(c)
	}
}
func TestWakeExitsTwoOnMessage(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	var stderr bytes.Buffer
	e.Stderr = &stderr
	go func() {
		time.Sleep(40 * time.Millisecond)
		_, _ = b.Send(bus.Message{From: "other", To: session.Address(session.Lead{ID: id(e), Target: config.Target{Agent: e.Agent}}), K: "done", S: "wake"})
	}()
	if code := Wake(ctx, e); code != 2 || !strings.Contains(stderr.String(), "wake") {
		t.Fatal(code, stderr.String())
	}
	lead(t, r, e)
}
func TestWakeTimesOut(t *testing.T) {
	e, _, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if code := Wake(ctx, e); code != 0 {
		t.Fatal(code)
	}
}
func TestHookNeverFailsLead(t *testing.T) {
	e, r, _ := setup(t, "claude")
	write(t, filepath.Join(r.Dir, "bus.ndjson"), "bad\n")
	for _, fn := range []func(context.Context, Env) Output{SessionStart, Prompt, PostTool} {
		silent(t, fn(context.Background(), e))
	}
}
func TestCodexPromptRegistersBeforeSessionStart(t *testing.T) {
	e, r, _ := setup(t, "codex")
	silent(t, Prompt(context.Background(), e))
	lead(t, r, e)
}
func TestDeliverIdleCodex(t *testing.T) {
	bin := testbin.Install(t, "codex", testbin.Scenario{Stdout: "Queued"})
	e := session.Entry{Agent: "codex", ThreadID: "thread", PID: os.Getpid()}
	if err := DeliverIdle(context.Background(), e, []byte("exact\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bin + ".args.json")
	if err != nil || !strings.Contains(string(data), "thread") || !strings.Contains(string(data), "queue") {
		t.Fatal(string(data), err)
	}
	e.PID = 1 << 30
	if err := DeliverIdle(context.Background(), e, []byte("closed")); err == nil {
		t.Fatal("closed queue accepted")
	}
}

func TestCodexRegistersWithLargeMetadataHeader(t *testing.T) {
	e, r, _ := setup(t, "codex")
	header, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"originator": "codex-tui", "instructions": strings.Repeat("x", 32*1024)}})
	write(t, transcript(e), string(header)+"\n"+string(fixture(t, "transcripts/codex-turn-context.trimmed.jsonl")))
	SessionStart(context.Background(), e)
	lead(t, r, e)
}
func TestWakeStopsAfterSessionEnd(t *testing.T) {
	e, _, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	var stderr bytes.Buffer
	e.Stderr = &stderr
	ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		time.Sleep(40 * time.Millisecond)
		SessionEnd(context.Background(), e)
		_, _ = b.Send(bus.Message{From: "other", To: session.Address(session.Lead{ID: id(e), Target: config.Target{Agent: e.Agent}}), K: "done", S: "too late"})
	}()
	if code := Wake(ctx, e); code != 0 || stderr.Len() != 0 {
		t.Errorf("ended session woke: %d %s", code, stderr.String())
	}
	<-finished
}
func TestCodexSandboxModeMapping(t *testing.T) {
	e, _, _ := setup(t, "codex")
	for native, want := range map[string]mode.Mode{"read-only": mode.Plan, "workspace-write": mode.Ask, "danger-full-access": mode.Full} {
		write(t, transcript(e), fmt.Sprintf("{\"type\":\"turn_context\",\"payload\":{\"sandbox_policy\":{\"type\":%q}}}\n", native))
		s, err := session.ReadLeadState("codex", transcript(e))
		if err != nil || s.Mode != want {
			t.Fatal(native, s, err)
		}
	}
}
func TestUnknownProcessMeansAlive(t *testing.T) {
	if !session.ProcessAlive(0) || !session.ProcessAlive(-1) || !session.ProcessAlive(os.Getpid()) {
		t.Fatal("unknown or current process treated as dead")
	}
}

func TestLateHooksCannotRegisterEndedLead(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			e, r, _ := setup(t, agent)
			SessionStart(context.Background(), e)
			SessionEnd(context.Background(), e)
			silent(t, Prompt(context.Background(), e))
			silent(t, SessionStart(context.Background(), e))
			if _, ok, err := r.Lead(id(e)); err != nil || ok {
				t.Fatal("ended lead recreated", err)
			}
		})
	}
}

func TestHookObservationStartTimeOrdersModes(t *testing.T) {
	for _, newerFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(newerFirst), func(t *testing.T) {
			e, r, _ := setup(t, "claude")
			older := change(t, e, "permission_mode", "full")
			older.StartedAt = time.Unix(100, 0)
			newer := change(t, e, "permission_mode", "plan")
			newer.StartedAt = older.StartedAt.Add(time.Second)
			if newerFirst {
				SessionStart(context.Background(), newer)
				SessionStart(context.Background(), older)
			} else {
				SessionStart(context.Background(), older)
				SessionStart(context.Background(), newer)
			}
			if l := lead(t, r, e); l.Mode != mode.Plan {
				t.Fatal("lost newer hook mode", l)
			}
			// Repeating plan must advance its observation time as well.
			newer.StartedAt = newer.StartedAt.Add(2 * time.Second)
			silent(t, Observe(context.Background(), newer))
			older.StartedAt = newer.StartedAt.Add(-time.Second)
			silent(t, Observe(context.Background(), older))
			if l := lead(t, r, e); l.Mode != mode.Plan {
				t.Fatal("unchanged restrictive hook was overwritten", l)
			}
		})
	}
}

func TestSessionEndBeforeInitialRegistration(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			e, r, _ := setup(t, agent)
			e.StartedAt = time.Unix(100, 0)
			ended := e
			ended.StartedAt = e.StartedAt.Add(time.Second)
			silent(t, SessionEnd(context.Background(), ended))
			// This hook started before SessionEnd, then completed registration.
			silent(t, SessionStart(context.Background(), e))
			if agent == "codex" {
				silent(t, Prompt(context.Background(), e))
			}
			if _, ok, err := r.Lead(id(e)); err != nil || ok {
				t.Fatal("initial hook survived SessionEnd", err)
			}
		})
	}
}
