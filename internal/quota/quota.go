// Package quota reads subscription limits through the agents' own CLIs.
// Claude may use a minimal CLI probe when /usage supplies no windows.
// It never uses reset credits.
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
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// The read's data types live in core/account, which the paid-runs rules
// read without reading anything themselves.
type (
	Window     = account.Window
	ExtraUsage = account.ExtraUsage
	Credits    = account.Credits
	Snapshot   = account.Snapshot
)

type Reader struct {
	Version string
	// Timeout bounds each agent, including startup, auth status and shutdown.
	Timeout time.Duration
	// IdentitySink receives the transient identity fields of a free reply,
	// one call per kind, straight from the decoder: raw is nil when the reply
	// has none. Nothing else keeps them. A read whose reply never arrived
	// calls no sink, and a sink error fails the read. Nil skips identity.
	IdentitySink func(agent, kind string, raw []byte) error
	// Once reads without the retry of a timed-out read, for a caller that can
	// do without the answer and should not wait twice for it.
	Once bool
	// NoProbe never starts the Claude model probe, for a caller that reads
	// often and can do without the answer, such as the pace balance.
	NoProbe bool
}

// Identity kinds handed to Reader.IdentitySink: the login's account, and the
// organization or workspace it belongs to.
const (
	IdentityAccount = account.IdentityAccount
	IdentityOrg     = account.IdentityOrg
)

// errIdentity hides the sink's error, which may quote the identity.
var errIdentity = errors.New("account identity not recorded")

// ErrTimedOut is the error of a read that did not end in its budget, or
// whose caller gave up on it. It found nothing, so it is transient.
var ErrTimedOut error = account.TransientError("read timed out or canceled")

// Read returns the agent's quota as a free worker sees it: the process that
// workerenv.Build makes from duolog's environment and the default
// env_passthrough. Callers that know the launch read with ReadProcess.
func (r Reader) Read(ctx context.Context, agent string) (Snapshot, error) {
	cfg := &config.Config{EnvPassthrough: config.DefaultEnvPassthrough(runtime.GOOS)}
	p, err := workerenv.Build(workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: workerenv.Free,
		Config: cfg, Environ: os.Environ()})
	if err != nil {
		return Snapshot{}, errors.New("unknown agent")
	}
	return r.ReadProcess(ctx, p)
}

const (
	// retryFactor lengthens the budget of the one retry of a timed-out read.
	retryFactor = 2
	// retryPause is the wait before the retry of a read that failed at once,
	// long enough for another read of the same account to end.
	retryPause = 500 * time.Millisecond
)

// ReadProcess returns the quota that process p sees. Every read runs p's
// argv, its prefix and settings included, with exactly p's environment, and
// finds the CLI in that environment's PATH. On an error the snapshot holds
// only the billing evidence the read saw, such as a reply without windows.
//
// A read that runs past its own budget, or whose Claude auth status runs
// past its bound, is read once more with both budgets retryFactor times as
// long: on a loaded machine each cold start of the CLI can take seconds,
// and a Claude read starts it twice in a row. Any other transient failure is
// read once more after retryPause, with the same budgets. A caller that ends
// ctx, or a Reader with Once set, gets no retry. A retry that fails again is
// a failed read, as the first one was.
func (r Reader) ReadProcess(ctx context.Context, p workerenv.Process) (Snapshot, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	probeUsed := r.NoProbe
	s, err := r.readOnce(ctx, p, timeout, claudeAuthTimeout, &probeUsed)
	if r.Once || ctx.Err() != nil {
		return s, err
	}
	switch {
	case errors.Is(err, ErrTimedOut) || err == nil && timedOut(s.Billing):
		return r.readOnce(ctx, p, retryFactor*timeout, retryFactor*claudeAuthTimeout, &probeUsed)
	case account.IsTransient(err) && pause(ctx, retryPause):
		return r.readOnce(ctx, p, timeout, claudeAuthTimeout, &probeUsed)
	}
	return s, err
}

// pause waits for d and reports false when ctx ended first.
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// readOnce is one read of p, bounded by timeout, with auth status bounded by
// authTimeout.
func (r Reader) readOnce(ctx context.Context, p workerenv.Process, timeout, authTimeout time.Duration, probeUsed *bool) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if len(p.Prefix) == 0 {
		return Snapshot{}, errors.New("not installed")
	}
	bin, err := lookPath(p.Prefix[0], p.Env)
	if err != nil {
		return Snapshot{}, errors.New("not installed")
	}
	dir, err := os.MkdirTemp("", "duolog-quota-")
	if err != nil {
		return Snapshot{}, errors.New("cannot create temporary folder")
	}
	defer os.RemoveAll(dir)
	run := runner{p: p, bin: bin, dir: dir, authTimeout: authTimeout, probeUsed: probeUsed}
	if sink := r.IdentitySink; sink != nil {
		run.identity = func(kind string, raw []byte) error { return sink(p.Agent, kind, raw) }
	}
	var s Snapshot
	switch p.Agent {
	case "claude":
		s, err = readClaude(ctx, run)
	case "codex":
		s, err = r.readCodex(ctx, run)
	default:
		err = errors.New("unknown agent")
	}
	if ctx.Err() != nil {
		return Snapshot{}, ErrTimedOut
	}
	if err != nil {
		return Snapshot{Billing: s.Billing}, err
	}
	if !s.Recalled {
		s.ReadAt = time.Now().UTC()
	}
	return s, nil
}

// lookPath finds name in the PATH of env, never in duolog's own. A name with
// a separator is used as it is.
func lookPath(name string, env []string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return exec.LookPath(name)
	}
	path := ""
	for _, pair := range env {
		key, value, _ := strings.Cut(pair, "=")
		if key == "PATH" || runtime.GOOS == "windows" && strings.EqualFold(key, "PATH") {
			path = value
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if bin, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return bin, nil
		}
	}
	return "", errors.New("not installed")
}

// runner starts the read subprocesses of one process from its executable, in
// a temporary folder. authTimeout bounds Claude's auth status.
type runner struct {
	probeUsed   *bool
	p           workerenv.Process
	bin, dir    string
	identity    func(kind string, raw []byte) error
	authTimeout time.Duration
}

// identify hands one reply's identity fields to the sink, account first.
func (r runner) identify(account, org []byte) error {
	if r.identity == nil {
		return nil
	}
	if r.identity(IdentityAccount, account) != nil || r.identity(IdentityOrg, org) != nil {
		return errIdentity
	}
	return nil
}

// command runs the process's argv with args, and exactly its environment.
func (r runner) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.bin, r.p.Argv(args...)[1:]...)
	cmd.Dir = r.dir
	cmd.WaitDelay = 200 * time.Millisecond
	configureProcess(cmd)
	// Non-nil, so that an empty environment never means duolog's own.
	cmd.Env = append([]string{}, r.p.Env...)
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
	case "free", "pro", "max", "ultra", "plus", "team", "business", "enterprise", "edu":
		return value
	}
	return ""
}

// claudePlan is the plan of a Claude auth status. Claude names the $200 plan
// "max", as the $100 one, and tells them apart only by planTier "max_20x".
func claudePlan(subscription, tier string) string {
	if subscription == "max" && tier == "max_20x" {
		return "ultra"
	}
	return plan(subscription)
}

// rejected reports whether a severity refuses runs, as Window.Rejected does.
func rejected(value string) bool { return Window{Severity: value}.Rejected() }

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
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait() // the exit status of a stopped server does not matter
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		} else {
			_ = cmd.Process.Kill()
		}
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
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		// Notifications and server requests carry a method; only a reply
		// answers the id.
		if json.Unmarshal(scanner.Bytes(), &reply) != nil || reply.Method != "" || reply.ID != id {
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

func (r Reader) readCodex(ctx context.Context, run runner) (Snapshot, error) {
	cmd := run.command(ctx, "app-server")
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
	call := func(id int, method string, params any) (json.RawMessage, error) {
		request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if params != nil {
			request["params"] = params
		}
		if err := rpcWrite(stdin, request); err != nil {
			return nil, err
		}
		return rpcReply(scanner, id)
	}
	// Free reads only: no model turn, no reset credit, no token and no
	// opt-in capability. A failed account, config or auth read is missing
	// route evidence; only the rate limit read decides whether the read
	// failed.
	account, accountPlan := unreadFacts("account:unread", codexAccountLayer), ""
	config := unreadFacts("provider:unread", codexConfigLayer)
	auth := unreadFacts("auth:unread", codexAuthLayer)
	if data, err := call(codexAccountID, "account/read", map[string]any{"refreshToken": false}); err == nil {
		account, accountPlan = readCodexAccount(data)
		if err := run.identify(codexIdentity(data)); err != nil {
			return Snapshot{Billing: codexBilling(BillingFacts{}, account, config, auth)}, err
		}
	}
	data, limitsErr := call(codexRateLimitsID, "account/rateLimits/read", nil)
	if reply, err := call(codexConfigID, "config/read", map[string]any{"includeLayers": false}); err == nil {
		config, _ = parseCodexConfig(reply)
	}
	if reply, err := call(codexAuthID, "getAuthStatus", map[string]any{"includeToken": false, "refreshToken": false}); err == nil {
		auth, _ = parseCodexAuth(reply)
	}
	if limitsErr != nil {
		return Snapshot{Billing: codexBilling(BillingFacts{}, account, config, auth)}, limitsErr
	}
	s, err := parseCodex(data)
	s.Billing = codexBilling(settleCodexSeat(s.Billing, data, account, accountPlan), account, config, auth)
	return s, err
}

const (
	codexRateLimitsID = 2
	codexAccountID    = 3
	codexConfigID     = 4
	codexAuthID       = 5
)

type codexLimit struct {
	PlanType  string       `json:"planType"`
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	Credits   *Credits     `json:"credits"`
	facts     codexLimitFacts
}

func (l *codexLimit) UnmarshalJSON(data []byte) error {
	type plain codexLimit
	if err := json.Unmarshal(data, (*plain)(l)); err != nil {
		return err
	}
	var f fields
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	l.facts = readCodexLimitFacts(f)
	return nil
}

type codexWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins int      `json:"windowDurationMins"`
	ResetsAt           int64    `json:"resetsAt"`
}

func (l codexLimit) blocked() bool {
	v := string(l.facts.reachedType)
	o := l.facts.ordinary.value
	return l.facts.spendReached || (o != nil && !*o) || (v != "" && v != "null" && v != `"none"` && v != `""`)
}

func (l codexLimit) windows() ([]Window, error) { return l.windowsBeside(false) }

// windowsBeside reads the limit's windows. A bad individual limit beside a
// valid window, this limit's or, with others, one of the reply's, is left out,
// so that it cannot stop an account that works. With no other window it fails
// the read.
func (l codexLimit) windowsBeside(others bool) ([]Window, error) {
	var result []Window
	for _, w := range []*codexWindow{l.Primary, l.Secondary} {
		if w == nil {
			continue
		}
		if w.UsedPercent == nil || *w.UsedPercent < 0 || *w.UsedPercent > 100 || w.WindowDurationMins <= 0 {
			return nil, errors.New("invalid quota window")
		}
		kind := fmt.Sprintf("%dm", w.WindowDurationMins)
		switch w.WindowDurationMins {
		case 300:
			kind = "session"
		case 10080:
			kind = "weekly_all"
		}
		reset := time.Time{}
		if w.ResetsAt > 0 {
			reset = time.Unix(w.ResetsAt, 0).UTC()
		}
		result = append(result, Window{Kind: kind, DurationMinutes: w.WindowDurationMins, UsedPercent: *w.UsedPercent, ResetsAt: reset})
	}
	if l.facts.individual != nil {
		w, err := l.facts.individual.window()
		if err != nil && len(result) == 0 && !others {
			return nil, err
		}
		if err == nil {
			result = append(result, w)
		}
	}
	return result, nil
}

func parseCodex(data []byte) (Snapshot, error) {
	var reply struct {
		RateLimits *codexLimit           `json:"rateLimits"`
		ByID       map[string]codexLimit `json:"rateLimitsByLimitId"`
	}
	var top fields
	if json.Unmarshal(data, &reply) != nil || json.Unmarshal(data, &top) != nil || reply.RateLimits == nil {
		return Snapshot{}, errors.New("no quota data; check login")
	}
	l := reply.RateLimits
	windows, err := l.windows()
	if err != nil {
		return Snapshot{}, err
	}
	// Permission is the reply's own value, which Codex 0.165 sends beside
	// the limits, joined with the account limit's: the stricter one wins. A
	// duplicate limit can only take it away: a sparse null never clears a
	// block, and a later read is the only way back. rateLimitResetCredits
	// is never read: duolog neither counts nor spends a reset.
	ordinary := joinPermissions(readCodexPermission(top), l.facts.ordinary)
	billing := BillingFacts{OrdinaryUsageAllowed: ordinary, ResetCredits: l.facts.resetCredits}
	addCodexLimit(&billing, l.facts)
	if len(windows) == 0 {
		return Snapshot{Billing: billing}, errors.New("no quota windows")
	}
	refused := ordinary != nil && !*ordinary
	s := Snapshot{Plan: plan(l.PlanType), Windows: windows, Credits: l.Credits, Rejected: l.blocked() || refused, SpendControlReached: l.facts.spendReached, Billing: billing, ModelLimits: make(map[string][]Window)}
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
		ws, err := limit.windowsBeside(len(windows) > 0)
		if err != nil {
			return Snapshot{}, err
		}
		addCodexLimit(&s.Billing, limit.facts)
		// A limit without windows of its own, or with the account's, is the
		// account's limit. A scoped block stays with its family's windows,
		// so the account and the other families keep their state.
		if len(ws) == 0 || slices.Equal(ws, windows) {
			s.Rejected = s.Rejected || limit.blocked()
			if o := limit.facts.ordinary.value; o != nil && !*o {
				s.Billing.OrdinaryUsageAllowed = o
			}
			continue
		}
		if limit.blocked() {
			for i := range ws {
				ws[i].Severity = "rejected"
			}
		}
		family := route.Normalize("codex", id)
		switch family {
		case "codex", "sol", "terra", "luna", "sonnet", "opus", "haiku":
		default:
			family = "other"
		}
		s.ModelLimits[family] = append(s.ModelLimits[family], ws...)
	}
	return allowIndividualSeat(s, l, readCodexPermission(top), reply.ByID), nil
}
