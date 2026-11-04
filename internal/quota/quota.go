// Package quota reads subscription limits through the agents' own CLIs.
// It never starts a model request or uses reset credits.
package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/models"
)

type Window struct {
	Kind            string    `json:"kind"`
	Group           string    `json:"group,omitempty"`
	Model           string    `json:"model,omitempty"`
	DurationMinutes int       `json:"duration_minutes,omitempty"`
	UsedPercent     float64   `json:"used_percent"`
	ResetsAt        time.Time `json:"resets_at"`
	Severity        string    `json:"severity,omitempty"`
	Active          bool      `json:"active,omitempty"`
}

type ExtraUsage struct {
	Enabled      bool     `json:"enabled"`
	UsedCredits  float64  `json:"used_credits"`
	MonthlyLimit *float64 `json:"monthly_limit"`
}

type Credits struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance,omitempty"`
}

// Snapshot contains quota fields only; identity and auth fields are discarded.
type Snapshot struct {
	Plan                string              `json:"plan,omitempty"`
	Windows             []Window            `json:"windows"`
	ModelLimits         map[string][]Window `json:"model_limits,omitempty"`
	ExtraUsage          *ExtraUsage         `json:"extra_usage,omitempty"`
	Credits             *Credits            `json:"credits,omitempty"`
	Rejected            bool                `json:"rejected"`
	SpendControlReached bool                `json:"spend_control_reached,omitempty"`
	ReadAt              time.Time           `json:"read_at"`
}

// Healthy requires current, explicit headroom in every reported window.
func (s Snapshot) Healthy(now time.Time) bool {
	if s.Rejected || s.SpendControlReached || len(s.Windows) == 0 {
		return false
	}
	check := func(windows []Window) bool {
		for _, w := range windows {
			if w.UsedPercent < 0 || w.UsedPercent >= 100 || !w.ResetsAt.After(now) || rejected(w.Severity) {
				return false
			}
		}
		return true
	}
	if !check(s.Windows) {
		return false
	}
	for _, windows := range s.ModelLimits {
		if !check(windows) {
			return false
		}
	}
	return true
}

type Reader struct {
	Version string
	// Timeout bounds each agent, including startup, auth status and shutdown.
	Timeout time.Duration
}

func (r Reader) Read(ctx context.Context, agent string) (Snapshot, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bin, err := exec.LookPath(agent)
	if err != nil {
		return Snapshot{}, errors.New("not installed")
	}
	dir, err := os.MkdirTemp("", "duolog-quota-")
	if err != nil {
		return Snapshot{}, errors.New("cannot create temporary folder")
	}
	defer os.RemoveAll(dir)
	var s Snapshot
	switch agent {
	case "claude":
		s, err = readClaude(ctx, bin, dir)
	case "codex":
		s, err = r.readCodex(ctx, bin, dir)
	default:
		err = errors.New("unknown agent")
	}
	if ctx.Err() != nil {
		return Snapshot{}, errors.New("read timed out or canceled")
	}
	if err != nil {
		return Snapshot{}, err
	}
	s.ReadAt = time.Now().UTC()
	return s, nil
}

func command(ctx context.Context, bin, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 200 * time.Millisecond
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "DUOLOG_DEPTH=") && !strings.HasPrefix(value, "DUOLOG_LEAD=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "DUOLOG_DEPTH=1", "DUOLOG_LEAD=")
	// Never surface stderr: it can contain account details or credentials.
	cmd.Stderr = io.Discard
	return cmd
}

func scanLines(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 4*1024*1024)
	return s
}

func plan(value string) string {
	switch value {
	case "free", "pro", "max", "plus", "team", "business", "enterprise", "edu":
		return value
	}
	return ""
}

func rejected(value string) bool {
	switch value {
	case "rejected", "blocked", "exceeded":
		return true
	}
	return false
}

func safeSeverity(value string) string {
	switch value {
	case "normal", "warning", "allowed", "allowed_warning", "rejected", "blocked", "exceeded":
		return value
	}
	return "unknown"
}

// Stop and reap even if a server ignores EOF or never answers the requested id.
func stop(cmd *exec.Cmd, stdin io.Closer) {
	if stdin != nil {
		_ = stdin.Close()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		_ = cmd.Process.Kill()
		<-done
	}
}

func rpcWrite(w io.Writer, value any) error {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return errors.New("app-server request failed")
	}
	return nil
}

func rpcReply(scanner *bufio.Scanner, id int) (json.RawMessage, error) {
	for scanner.Scan() {
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &reply) != nil || reply.ID != id {
			continue
		}
		if len(reply.Error) > 0 && string(reply.Error) != "null" {
			return nil, errors.New("app-server rejected quota request; check login")
		}
		if len(reply.Result) == 0 || string(reply.Result) == "null" {
			return nil, errors.New("app-server returned no quota data")
		}
		return reply.Result, nil
	}
	return nil, errors.New("app-server ended without a quota reply")
}

func (r Reader) readCodex(ctx context.Context, bin, dir string) (Snapshot, error) {
	cmd := command(ctx, bin, dir, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Snapshot{}, errors.New("cannot open app-server input")
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Snapshot{}, errors.New("cannot open app-server output")
	}
	if err = cmd.Start(); err != nil {
		return Snapshot{}, errors.New("cannot start app-server")
	}
	closeOnCancel := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer closeOnCancel()
	defer stop(cmd, stdin)
	init := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "duolog", "version": r.Version}}}
	if err = rpcWrite(stdin, init); err != nil {
		return Snapshot{}, err
	}
	scanner := scanLines(stdout)
	if _, err = rpcReply(scanner, 1); err != nil {
		return Snapshot{}, err
	}
	if err = rpcWrite(stdin, map[string]any{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		return Snapshot{}, err
	}
	if err = rpcWrite(stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "account/rateLimits/read"}); err != nil {
		return Snapshot{}, err
	}
	data, err := rpcReply(scanner, 2)
	if err != nil {
		return Snapshot{}, err
	}
	return parseCodex(data)
}

type codexLimit struct {
	PlanType             string          `json:"planType"`
	Primary              *codexWindow    `json:"primary"`
	Secondary            *codexWindow    `json:"secondary"`
	Credits              *Credits        `json:"credits"`
	SpendControlReached  bool            `json:"spendControlReached"`
	OrdinaryUsageAllowed *bool           `json:"ordinaryUsageAllowed"`
	RateLimitReachedType json.RawMessage `json:"rateLimitReachedType"`
}
type codexWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins int      `json:"windowDurationMins"`
	ResetsAt           int64    `json:"resetsAt"`
}

func (l codexLimit) blocked() bool {
	v := string(l.RateLimitReachedType)
	return l.SpendControlReached || (l.OrdinaryUsageAllowed != nil && !*l.OrdinaryUsageAllowed) || (v != "" && v != "null" && v != `"none"` && v != `""`)
}

func (l codexLimit) windows() ([]Window, error) {
	var result []Window
	for _, w := range []*codexWindow{l.Primary, l.Secondary} {
		if w == nil {
			continue
		}
		if w.UsedPercent == nil || *w.UsedPercent < 0 || *w.UsedPercent > 100 || w.WindowDurationMins <= 0 {
			return nil, errors.New("invalid quota window")
		}
		kind := fmt.Sprintf("%dm", w.WindowDurationMins)
		if w.WindowDurationMins == 300 {
			kind = "session"
		} else if w.WindowDurationMins == 10080 {
			kind = "weekly_all"
		}
		reset := time.Time{}
		if w.ResetsAt > 0 {
			reset = time.Unix(w.ResetsAt, 0).UTC()
		}
		result = append(result, Window{Kind: kind, DurationMinutes: w.WindowDurationMins, UsedPercent: *w.UsedPercent, ResetsAt: reset})
	}
	return result, nil
}

func parseCodex(data []byte) (Snapshot, error) {
	var reply struct {
		RateLimits *codexLimit           `json:"rateLimits"`
		ByID       map[string]codexLimit `json:"rateLimitsByLimitId"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.RateLimits == nil {
		return Snapshot{}, errors.New("no quota data; check login")
	}
	l := reply.RateLimits
	windows, err := l.windows()
	if err != nil {
		return Snapshot{}, err
	}
	if len(windows) == 0 {
		return Snapshot{}, errors.New("no quota windows")
	}
	s := Snapshot{Plan: plan(l.PlanType), Windows: windows, Credits: l.Credits, Rejected: l.blocked(), SpendControlReached: l.SpendControlReached, ModelLimits: make(map[string][]Window)}
	if s.Credits != nil && s.Credits.Balance != nil {
		var number json.Number
		if json.Unmarshal([]byte(*s.Credits.Balance), &number) != nil {
			s.Credits.Balance = nil
		}
	}
	ids := make([]string, 0, len(reply.ByID))
	for id := range reply.ByID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		limit := reply.ByID[id]
		ws, err := limit.windows()
		if err != nil {
			return Snapshot{}, err
		}
		s.Rejected = s.Rejected || limit.blocked()
		if slices.Equal(ws, windows) {
			continue
		}
		family := models.Normalize("codex", id)
		switch family {
		case "codex", "sol", "terra", "luna", "sonnet", "opus", "haiku":
		default:
			family = "other"
		}
		s.ModelLimits[family] = append(s.ModelLimits[family], ws...)
	}
	return s, nil
}
