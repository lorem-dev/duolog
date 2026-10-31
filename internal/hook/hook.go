// Package hook delivers bus lines without spending tokens on empty inboxes.
package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/roster"
	"github.com/lorem-dev/duolog/internal/session"
)

// Env makes hook stdin, environment and output explicit for embedded callers.
// Root is the shared project root. Nil Environ uses the process environment.
type Env struct {
	Root, Agent string
	Input       []byte
	Environ     []string
	Stderr      io.Writer
}

type Output struct {
	Stdout, Stderr []byte
	Code           int
}

type input struct {
	ID             string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Model          string `json:"model"`
	PermissionMode string `json:"permission_mode"`
}

type state struct {
	cfg  *config.Config
	reg  *session.Registry
	bus  *bus.Bus
	lead session.Lead
}

func value(env []string, key string) string {
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, key+"="); ok {
			return v
		}
	}
	return ""
}
func environment(e Env) []string {
	if e.Environ == nil {
		return os.Environ()
	}
	return e.Environ
}
func worker(e Env) bool {
	depth, _ := strconv.Atoi(value(environment(e), "DUOLOG_DEPTH"))
	return depth >= 1
}

// prepare refuses uninitialized projects before any library can create state.
func prepare(e Env, register bool) (*state, error) {
	if worker(e) || (e.Agent != "claude" && e.Agent != "codex") {
		return nil, nil
	}
	dir := filepath.Join(e.Root, ".duolog")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	var h input
	if json.Unmarshal(e.Input, &h) != nil || h.ID == "" {
		return nil, nil
	}
	cfg, err := config.Load(e.Root)
	if err != nil {
		return nil, err
	}
	reg, err := session.Open(dir)
	if err != nil {
		return nil, err
	}
	lead, known, err := reg.Lead(h.ID)
	if err != nil {
		return nil, err
	}
	if known && lead.Agent != e.Agent {
		return nil, nil
	}
	if !known {
		if !register {
			return nil, nil
		}
		interactive := value(environment(e), "CLAUDE_CODE_ENTRYPOINT") == "cli"
		if e.Agent == "codex" {
			interactive = session.Originator(h.TranscriptPath) == "codex-tui"
		}
		if !interactive {
			return nil, nil
		}
		lead = session.Lead{ID: h.ID, Target: config.Target{Agent: e.Agent}, Mode: mode.Ask, PID: os.Getppid()}
	}
	before := lead
	if h.TranscriptPath != "" {
		lead.TranscriptPath = h.TranscriptPath
	}
	observed, _ := session.ReadLeadState(e.Agent, lead.TranscriptPath)
	lead.Model, lead.Effort = observed.Model, observed.Effort
	if lead.Model == "" {
		lead.Model = h.Model
	}
	if e.Agent == "claude" {
		if h.PermissionMode != "" {
			lead.Mode = mode.Parse(e.Agent, h.PermissionMode)
		}
	} else if observed.Mode != "" {
		lead.Mode = observed.Mode
	}
	if known {
		lead, err = reg.UpdateLead(before, lead)
	} else {
		lead, err = reg.StartLead(lead)
	}
	if err != nil {
		return nil, err
	}
	b, err := bus.Open(dir, cfg.Wire)
	if err != nil {
		return nil, err
	}
	return &state{cfg: cfg, reg: reg, bus: b, lead: lead}, nil
}

func receive(s *state) ([]byte, error) {
	lines, more, err := s.bus.InboxLimited(session.Address(s.lead), 10)
	if err != nil {
		return nil, err
	}
	data := bytes.Join(lines, nil)
	if more {
		data = append(data, []byte("More messages: duolog inbox --reader "+session.Address(s.lead)+"\n")...)
	}
	return data, nil
}

func render(agent, event string, data []byte) Output {
	if len(data) == 0 {
		return Output{}
	}
	// Both agents use the hookSpecificOutput envelope for these synchronous
	// events; asyncRewake instead requires exit 2 and plain stderr.
	if agent != "claude" && agent != "codex" {
		return Output{}
	}
	encoded, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": string(data)}})
	return Output{Stdout: append(encoded, '\n')}
}
func deliver(ctx context.Context, e Env, event string, register, includeRoster bool) Output {
	if ctx.Err() != nil {
		return Output{}
	}
	s, err := prepare(e, register)
	if err != nil || s == nil {
		return Output{}
	}
	data, err := receive(s)
	if err != nil {
		return Output{}
	}
	if includeRoster {
		data = append([]byte(roster.Line(s.cfg, s.lead.ID, s.reg)+"\n"), data...)
	}
	return render(e.Agent, event, data)
}
func SessionStart(ctx context.Context, e Env) Output {
	return deliver(ctx, e, "SessionStart", true, true)
}
func Prompt(ctx context.Context, e Env) Output {
	return deliver(ctx, e, "UserPromptSubmit", e.Agent == "codex", false)
}
func PostTool(ctx context.Context, e Env) Output { return deliver(ctx, e, "PostToolUse", false, false) }

// Observe records lead permission changes on hooks which have no delivery
// output, including PreToolUse and PermissionRequest handled by the gate.
func Observe(ctx context.Context, e Env) Output {
	if ctx.Err() == nil {
		_, _ = prepare(e, false)
	}
	return Output{}
}
func SessionEnd(ctx context.Context, e Env) Output {
	if ctx.Err() != nil || worker(e) {
		return Output{}
	}
	if info, err := os.Stat(filepath.Join(e.Root, ".duolog")); err != nil || !info.IsDir() {
		return Output{}
	}
	var h input
	if json.Unmarshal(e.Input, &h) != nil || h.ID == "" {
		return Output{}
	}
	reg, err := session.Open(filepath.Join(e.Root, ".duolog"))
	if err != nil {
		return Output{}
	}
	if lead, ok, err := reg.Lead(h.ID); err == nil && ok && lead.Agent == e.Agent {
		_ = reg.Close(h.ID)
	}
	return Output{}
}

// Wake consumes the same inbox cursor as Prompt before waking Claude, so the
// UserPromptSubmit caused by asyncRewake cannot deliver a line a second time.
func Wake(ctx context.Context, e Env) int {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	s, err := prepare(e, false)
	if err != nil || s == nil {
		return 0
	}
	path := filepath.Join(e.Root, ".duolog", "bus.ndjson")
	size := func() int64 {
		info, err := os.Stat(path)
		if err != nil {
			return -1
		}
		return info.Size()
	}
	previous := int64(-1)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return 0
		}
		if _, alive, err := s.reg.Lead(s.lead.ID); err != nil || !alive {
			return 0
		}
		current := size()
		if current != previous {
			previous = current
			data, err := receive(s)
			if err != nil {
				return 0
			}
			if len(data) > 0 {
				stderr := e.Stderr
				if stderr == nil {
					stderr = os.Stderr
				}
				if _, err = stderr.Write(data); err != nil {
					return 0
				}
				return 2
			}
		}
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}

// DeliverIdle checks a known PID before queueing: codex queue reports success
// even for a closed session. A successful command is only a submission receipt.
func DeliverIdle(ctx context.Context, entry session.Entry, line []byte) error {
	if entry.Closed || !session.ProcessAlive(entry.PID) {
		return fmt.Errorf("session %q has ended", entry.ID)
	}
	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdin = bytes.NewReader(nil)
		return cmd.Run()
	}
	if entry.Agent == "codex" {
		if entry.ThreadID == "" {
			return fmt.Errorf("missing Codex thread id")
		}
		return run("codex", "queue", "--thread", entry.ThreadID, string(line))
	}
	if entry.Agent != "claude" || entry.BlockID == "" {
		return fmt.Errorf("missing Claude pane")
	}
	if err := run("rex", "send", "--block", entry.BlockID, string(line)); err != nil {
		return err
	}
	timer := time.NewTimer(700 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return run("rex", "send-key", "--block", entry.BlockID, "enter")
}
