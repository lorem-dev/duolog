// Package hook delivers bus lines without spending tokens on empty inboxes.
package hook

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/roster"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

// Env makes hook stdin, environment and output explicit for embedded callers.
// Root is the shared project root. Nil Environ uses the process environment.
type Env struct {
	Root, Agent string
	WorkRoot    string
	Input       []byte
	Environ     []string
	Stderr      io.Writer
	// StartedAt is captured by the command before it reads hook stdin.
	// Embedded callers may omit it; the hook then captures its own start time.
	StartedAt time.Time
	// Peers returns the lines of `duolog peer list` for the lead with this
	// session id. Nil means none; the command sets it.
	Peers func(ctx context.Context, leadID string) []string
	// Load returns the load of the lead with this session id: its peers, the
	// session limits and the 5-hour window. Nil means none; the command sets it.
	Load func(ctx context.Context, leadID string) load.Summary
	// PeerIdle is the hook point for work that follows a peer going idle, such
	// as starting queued work. The lead's hooks call it once for each
	// peer-idle message they deliver, with the lead's session id and the peer
	// id. They run with the lead's environment; the peer's own Stop
	// hook runs as a worker and cannot open peers. Nil by default.
	PeerIdle func(ctx context.Context, leadID, peerID string)
	// QueueRun starts the queue run of the lead with this session id when
	// the lead has queued items. The lead's prompt hook calls it. Nil means
	// none; the command sets it.
	QueueRun func(ctx context.Context, leadID string)
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
	// AgentID is set when a Claude subagent makes the call. The subagent
	// shares its lead's session id, but its permission_mode is its own.
	AgentID string `json:"agent_id"`
	// Source is why a Claude session started: startup, resume, clear or compact.
	Source string `json:"source"`
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

func startTime(e Env) time.Time {
	if !e.StartedAt.IsZero() {
		return e.StartedAt
	}
	return time.Now()
}

func interactiveLead(e Env, h input) bool {
	if e.Agent == "codex" {
		return session.Originator(h.TranscriptPath) == "codex-tui"
	}
	return value(environment(e), "CLAUDE_CODE_ENTRYPOINT") == "cli"
}

// prepare refuses uninitialized projects before any library can create state.
func prepare(e Env, register bool, started time.Time) (*state, error) {
	if worker(e) || (e.Agent != "claude" && e.Agent != "codex") {
		return nil, nil
	}
	var h input
	if json.Unmarshal(e.Input, &h) != nil || h.ID == "" {
		return nil, nil //nolint:nilerr // an event without a readable id is none of ours, so it is skipped
	}
	dir := filepath.Join(e.Root, ".duolog")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		// Only a session that will register may create state in a new folder.
		if !register || !interactiveLead(e, h) || !capability.HasDuolog(e.Agent, e.Root) {
			return nil, nil
		}
		if _, err := store.Open(e.Root); err != nil {
			return nil, err
		}
	}
	cfg, err := configfile.Load(e.Root)
	if err != nil {
		return nil, &configError{err}
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
		if !interactiveLead(e, h) {
			return nil, nil
		}
		lead = session.Lead{ID: h.ID, Target: config.Target{Agent: e.Agent}, Mode: mode.Ask, PID: session.LeadPID()}
	}
	if h.TranscriptPath != "" {
		lead.TranscriptPath = h.TranscriptPath
	}
	observed, _ := session.ReadLeadState(e.Agent, lead.TranscriptPath)
	if observed.Model != "" {
		lead.Model = observed.Model
	}
	if observed.Effort != "" {
		lead.Effort = observed.Effort
	}
	if lead.Model == "" {
		lead.Model = h.Model
	}
	// Only the lead's own main-thread payloads say its mode: a subagent's
	// definition can set a mode of its own, such as bypassPermissions.
	claudeMode := e.Agent == "claude" && h.PermissionMode != "" && h.AgentID == ""
	if claudeMode {
		lead.Mode = mode.Parse(e.Agent, h.PermissionMode)
	} else if e.Agent != "claude" && observed.Mode != "" {
		lead.Mode = observed.Mode
	}
	o := session.LeadObservation{ID: h.ID, Agent: e.Agent, Model: &lead.Model, Effort: &lead.Effort}
	if h.TranscriptPath != "" {
		o.TranscriptPath = &h.TranscriptPath
	}
	if !known {
		o.PID = &lead.PID
	}
	if claudeMode || (e.Agent == "codex" && observed.Mode != "") {
		o.Mode = &lead.Mode
	}
	if claudeMode {
		native := h.PermissionMode
		o.Native = &native
	}
	lead, err = reg.ObserveLead(o, started)
	if err != nil {
		return nil, err
	}
	b, err := bus.Open(dir, cfg.Wire)
	if err != nil {
		return nil, err
	}
	return &state{cfg: cfg, reg: reg, bus: b, lead: lead}, nil
}

// receive renders the lead's unread lines and names the peers of the
// peer-idle messages among them, each once.
func receive(s *state) (data []byte, idle []string, err error) {
	err = s.bus.Consume(session.Address(s.lead), func(held, unread [][]byte) [][]byte {
		var keep [][]byte
		data, keep = present(s.bus.Directory(), held, unread)
		idle = idlePeers(unread)
		return keep
	})
	return data, idle, err
}

// take receives the lead's unread lines, then calls PeerIdle for the peer-idle
// messages among them.
func take(ctx context.Context, e Env, s *state) ([]byte, error) {
	data, idle, err := receive(s)
	if err == nil {
		followIdle(ctx, e, s, idle)
	}
	return data, err
}

// idlePeers names the peers of the peer-idle messages in lines, in order and
// each once.
func idlePeers(lines [][]byte) []string {
	var peers []string
	for _, line := range lines {
		var m bus.Message
		if json.Unmarshal(line, &m) == nil && m.K == "peer-idle" && m.Ref != "" && !slices.Contains(peers, m.Ref) {
			peers = append(peers, m.Ref)
		}
	}
	return peers
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

// deliver renders the lead's unread bus lines, then extra's line. extra gets
// the lead's state, nil when the session is no registered lead, and the bus
// data delivered so far.
func deliver(ctx context.Context, e Env, event string, register, includeRoster bool, started time.Time, extra func(s *state, data []byte) string) Output {
	if ctx.Err() != nil {
		return Output{}
	}
	var data []byte
	s, err := prepare(e, register, started)
	if err == nil && s != nil {
		if event == "UserPromptSubmit" || event == "PostToolUse" {
			tellExited(ctx, e, s)
		}
		if data, err = take(ctx, e, s); err != nil {
			return Output{}
		}
		if includeRoster {
			data = append([]byte(roster.Line(s.cfg, s.lead.ID, s.reg)+roster.StartNote(e.Root, s.lead)+roster.LearningNote(s.cfg)+"\n"), data...)
		}
	} else {
		data = configLine(event, err)
	}
	if extra != nil {
		if line := extra(s, data); line != "" {
			data = append(data, []byte(line+"\n")...)
		}
	}
	return render(e.Agent, event, data)
}
func SessionStart(ctx context.Context, e Env) Output {
	return deliver(ctx, e, "SessionStart", true, true, startTime(e), func(*state, []byte) string {
		prune(e)
		var lines []string
		for _, line := range []string{telemetryStart(ctx, e), syncStart(e), compactStart(ctx, e)} {
			if line != "" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	})
}

// prune lets a lead's session start remove the delegation folders and
// artifacts that nothing reads any more. A worker never removes state.
func prune(e Env) {
	if worker(e) || (e.Agent != "claude" && e.Agent != "codex") {
		return
	}
	dir := filepath.Join(e.Root, ".duolog")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		store.Prune(dir, time.Now())
	}
}
func Prompt(ctx context.Context, e Env) Output {
	return deliver(ctx, e, "UserPromptSubmit", e.Agent == "codex", false, startTime(e), func(s *state, _ []byte) string {
		startQueue(ctx, e, s)
		return loadLine(ctx, e, s, false)
	})
}
func PostTool(ctx context.Context, e Env) Output {
	return deliver(ctx, e, "PostToolUse", false, false, startTime(e), func(s *state, data []byte) string {
		lines := []string{syncTool(e)}
		// The load line rides along with a bus delivery only.
		if len(data) > 0 {
			lines = append(lines, loadLine(ctx, e, s, true))
		}
		return strings.Join(slices.DeleteFunc(lines, func(l string) bool { return l == "" }), "\n")
	})
}

// Observe records lead permission changes on hooks which have no delivery
// output, including PreToolUse and PermissionRequest handled by the gate.
func Observe(ctx context.Context, e Env) Output {
	started := startTime(e)
	if ctx.Err() == nil {
		_, _ = prepare(e, false, started)
	}
	return Output{}
}
func SessionEnd(ctx context.Context, e Env) Output {
	started := startTime(e)
	if ctx.Err() != nil || worker(e) || (e.Agent != "claude" && e.Agent != "codex") {
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
	_, _ = reg.ObserveLead(session.LeadObservation{ID: h.ID, Agent: e.Agent, Ended: true}, started)
	return Output{}
}

// Wake consumes the same inbox cursor as Prompt before waking Claude, so the
// UserPromptSubmit caused by asyncRewake cannot deliver a line a second time.
func Wake(ctx context.Context, e Env) int {
	started := startTime(e)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// SessionStart starts this hook beside the one that registers the lead.
	s, err := prepare(e, false, started)
	for s == nil && err == nil && ctx.Err() == nil && time.Since(started) < 10*time.Second {
		time.Sleep(200 * time.Millisecond)
		s, err = prepare(e, false, started)
	}
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
			data, err := take(ctx, e, s)
			if err != nil {
				return 0
			}
			if len(data) > 0 {
				// The session can end while the bus is read; an ended one must not wake.
				if _, alive, err := s.reg.Lead(s.lead.ID); err != nil || !alive {
					return 0
				}
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
