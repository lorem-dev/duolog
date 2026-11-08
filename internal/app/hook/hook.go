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
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/roster"
	agentsync "github.com/lorem-dev/duolog/internal/app/sync"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

// Events is what one agent's hooks differ in: how a hook's context is handed
// back to the agent, and where the lead's permission mode is read from.
type Events interface {
	// Render wraps text in the output of the hook event, or returns nothing
	// when text is empty.
	Render(event string, text []byte) []byte
	// Mode reads the lead's permission mode from the hook payload or the
	// session transcript; false when this agent's hook says none.
	Mode(payload []byte, transcript string) (mode.Mode, bool)
}

// Lead is what the hooks ask of an agent's lead session.
type Lead interface {
	// Interactive reports whether the session is an interactive one in a
	// terminal, from its environment and its transcript.
	Interactive(environ []string, transcript string) bool
}

// Env makes hook stdin, environment and output explicit for embedded callers.
// Root is the shared project root. Nil Environ uses the process environment.
// Events and Lead are those of Agent; an agent without Events is not one the
// hooks serve, and every hook of it does nothing.
type Env struct {
	Root, Agent string
	Events      Events
	Lead        Lead
	// Agents are the registered agents' names, in registry order.
	Agents   []string
	WorkRoot string
	Input    []byte
	Environ  []string
	Stderr   io.Writer
	// StartedAt is captured by the command before it reads hook stdin.
	// Embedded callers may omit it; the hook then captures its own start time.
	StartedAt time.Time
	// Peers returns the lines of `duolog peer list` for the lead with this
	// session id. Nil means none; the command sets it.
	Peers func(ctx context.Context, leadID string) []string
	// Models resolves the Codex aliases of the sync that a hook runs. Nil means
	// an alias is an error; the command sets it.
	Models agentsync.ModelResolver
	// Sync holds the formats that duolog sync converts between. Nil means the
	// hook does not sync.
	Sync agentsync.Formats
	// Caps holds each registered agent's capabilities by name: their cached
	// inventories and whether duolog's hooks are in their settings. An agent
	// that is not in it has no duolog hooks and an unknown inventory.
	Caps map[string]roster.Caps
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
	// Config loads the project's config at root. Nil means the config files
	// of the project and the user.
	Config func(root string) (*config.Config, error)
	// InstallNote returns the note a lead's session start adds when the
	// project install at root is out of date, or nothing. It only reads local
	// files, and says nothing when its check fails. Nil means no note.
	InstallNote func(root string) string
	// RefreshSkills replaces the duolog skills at user scope that an older
	// duolog wrote, for a lead's session start in the project at root, and
	// returns a line that says so, or nothing. It never fails the hook. Nil
	// means no refresh.
	RefreshSkills func(root string) string
	// RestartNote returns the line that tells a lead to restart when install
	// wrote the user skills or the MCP server entry after since, the start of
	// its process, or nothing. It only stats local files. Nil means no note.
	RestartNote func(since time.Time) string
}

// loadConfig loads the config of the project at e.Root, through e.Config
// when it is set.
func loadConfig(e Env) (*config.Config, error) {
	if e.Config != nil {
		return e.Config(e.Root)
	}
	return configfile.Load(e.Root)
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
	return e.Lead != nil && e.Lead.Interactive(environment(e), h.TranscriptPath)
}

// served reports whether the hooks serve this agent: one without Events does
// nothing.
func served(e Env) bool { return e.Events != nil }

// prepare refuses uninitialized projects before any library can create state.
func prepare(e Env, register bool, started time.Time) (*state, error) {
	if worker(e) || !served(e) {
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
		if !register || !interactiveLead(e, h) || !roster.HasDuolog(e.Caps, e.Agent, e.Root) {
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
	// The agent says where its lead's mode is read from: for Claude only the
	// lead's own main-thread payloads (a subagent's definition can set a mode
	// of its own, such as bypassPermissions), for Codex the transcript.
	observedMode, hasMode := e.Events.Mode(e.Input, lead.TranscriptPath)
	if hasMode {
		lead.Mode = observedMode
	}
	o := session.LeadObservation{ID: h.ID, Agent: e.Agent, Model: &lead.Model, Effort: &lead.Effort}
	if h.TranscriptPath != "" {
		o.TranscriptPath = &h.TranscriptPath
	}
	if !known {
		o.PID = &lead.PID
	}
	if hasMode {
		o.Mode = &lead.Mode
	}
	// The native mode is recorded only when the payload itself carries the mode.
	if _, fromPayload := e.Events.Mode(e.Input, ""); fromPayload {
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
		data, keep = present(s.bus.Directory(), held, unread, time.Now())
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

// render hands data to the agent as the output of event. asyncRewake needs
// exit 2 and plain stderr instead, so Wake does not come through here.
func render(e Env, event string, data []byte) Output {
	if len(data) == 0 || !served(e) {
		return Output{}
	}
	return Output{Stdout: e.Events.Render(event, data)}
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
			data = append([]byte(roster.Line(s.cfg, e.Agents, s.lead.ID, s.reg)+roster.StartNote(e.Root, e.Agents, s.lead, e.Caps)+roster.LearningNote(s.cfg)+"\n"), data...)
		}
	} else {
		data = configLine(event, err)
	}
	if extra != nil {
		if line := extra(s, data); line != "" {
			data = append(data, []byte(line+"\n")...)
		}
	}
	return render(e, event, data)
}
func SessionStart(ctx context.Context, e Env) Output {
	return deliver(ctx, e, "SessionStart", true, true, startTime(e), func(s *state, _ []byte) string {
		prune(e)
		var lines []string
		// The skills are refreshed before the install is checked, so the note
		// sees them current.
		for _, line := range []string{telemetryStart(ctx, e), syncStart(e), compactStart(ctx, e), refreshSkills(e, s), installNote(e, s), restartNote(e, s, true)} {
			if line != "" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	})
}

// refreshSkills brings the user skills up to this duolog for a registered
// lead's session start, so that an update of the binary updates them. A
// worker and an unregistered session leave them, and a panic is no line.
func refreshSkills(e Env, s *state) (line string) {
	if s == nil || e.RefreshSkills == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			line = ""
		}
	}()
	return e.RefreshSkills(e.Root)
}

// installNote is the note of a registered lead's session start when the
// project install is out of date. A worker and an unregistered session get
// none, and a panic in the check is no note either.
func installNote(e Env, s *state) (note string) {
	if s == nil || e.InstallNote == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			note = ""
		}
	}()
	return e.InstallNote(e.Root)
}

// realStart reports whether a session start is a start of the agent's process,
// which loads the skills and the MCP server. A compaction or a clear goes on
// in the running process.
func realStart(e Env) bool {
	var h input
	return json.Unmarshal(e.Input, &h) == nil && slices.Contains([]string{"", "startup", "resume"}, h.Source)
}

// restartNote tells a registered lead once to restart when install wrote the
// user skills or the MCP server entry after its process started. A real start
// records the time in a marker file under .duolog/restart, and a prompt
// compares what install wrote with it, then moves the marker so the line comes
// once per install. A lead with no marker, as one started before markers
// existed, counts from its registered start, and without one it is told
// nothing; the marker is written either way. Any failure is no line, so a
// marker that cannot be written or a file dated in the future cannot repeat it.
func restartNote(e Env, s *state, start bool) (note string) {
	if s == nil || e.RestartNote == nil || s.lead.ID == "" {
		return ""
	}
	defer func() {
		if recover() != nil {
			note = ""
		}
	}()
	marker := filepath.Join(e.Root, ".duolog", "restart", filepath.Base(s.lead.ID))
	touch := func() bool {
		return os.MkdirAll(filepath.Dir(marker), 0o750) == nil && os.WriteFile(marker, nil, 0o600) == nil
	}
	if start {
		if realStart(e) {
			touch()
		}
		return ""
	}
	var since time.Time
	info, err := os.Stat(marker)
	if err == nil {
		since = info.ModTime()
	} else if since, err = s.reg.LeadRegisteredAt(s.lead.ID); err != nil || since.IsZero() {
		touch()
		return ""
	}
	line := e.RestartNote(since)
	if line == "" {
		if info == nil {
			touch()
		}
		return ""
	}
	// What is dated after this moment was not written by an install.
	if !touch() || e.RestartNote(time.Now()) != "" {
		return ""
	}
	return line
}

// prune lets a lead's session start remove the delegation folders and
// artifacts that nothing reads any more. A worker never removes state.
func prune(e Env) {
	if worker(e) || !served(e) {
		return
	}
	dir := filepath.Join(e.Root, ".duolog")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		store.Prune(dir, time.Now())
	}
}
func Prompt(ctx context.Context, e Env) Output {
	countTurn(ctx, e)
	return deliver(ctx, e, "UserPromptSubmit", e.Agent == "codex", false, startTime(e), func(s *state, _ []byte) string {
		startQueue(ctx, e, s)
		return strings.Join(slices.DeleteFunc([]string{loadLine(ctx, e, s, false), peerReportNote(e), restartNote(e, s, false)}, func(l string) bool { return l == "" }), "\n")
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
	if ctx.Err() != nil || worker(e) || !served(e) {
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
