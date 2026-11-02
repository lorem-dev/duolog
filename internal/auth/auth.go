// Package auth checks that a worker can sign in before it starts, and runs the
// agent's own login once when it cannot. No credential is printed or stored:
// the cache holds only the time of a passed check.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/store"
)

// Actions for capabilities.on_auth_failure.
const (
	LoginThenLead = "login_then_lead"
	Lead          = "lead"
	Fail          = "fail"
)

// DefaultLoginTimeout bounds a login the person may never finish.
const DefaultLoginTimeout = 3 * time.Minute

// cacheTTL keeps a passed check for a few minutes, so that a run of
// delegations does not ask the agent or the server every time.
const cacheTTL = 5 * time.Minute

// Problem is one credential that stops a worker. It never holds a value.
type Problem struct {
	// Agent is the worker's agent. Server is empty for the agent's own login.
	Agent, Server string
	// Static means the credential is fixed in config: there is nothing to log
	// in to, and Where names the config entry that holds it.
	Static bool
	Where  string
	// Reason is one short clause, such as "the server answered 401".
	Reason string
}

// Key names the problem in the lead's session record of declined logins.
func (p Problem) Key() string {
	if p.Server == "" {
		return "agent:" + p.Agent
	}
	return p.Agent + ":mcp:" + p.Server
}

// Need is what the lead gets in needs when the task goes back to it.
func (p Problem) Need() string {
	if p.Server == "" {
		return "auth:" + p.Agent
	}
	return capability.KindMCP + ":" + p.Server
}

// AuthNeed is the need for a login the lead must have the person run.
func (p Problem) AuthNeed() string {
	if p.Server == "" {
		return "auth:" + p.Agent
	}
	return "auth:mcp:" + p.Server
}

// Subject names the agent or the server in a line for the lead.
func (p Problem) Subject() string {
	if p.Server == "" {
		return p.Agent + " login"
	}
	return "mcp:" + p.Server + " in " + p.Agent
}

// LoginArgv is the agent's own login command, or nil for a static credential.
func (p Problem) LoginArgv() []string {
	if p.Static {
		return nil
	}
	switch {
	case p.Agent == "codex" && p.Server == "":
		return []string{"codex", "login"}
	case p.Agent == "claude" && p.Server == "":
		return []string{"claude", "auth", "login"}
	case p.Agent == "codex":
		return []string{"codex", "mcp", "login", p.Server}
	case p.Agent == "claude":
		return []string{"claude", "mcp", "login", p.Server}
	}
	return nil
}

// Command is the login as one line for the lead to show the person. The
// person runs it in a shell, so every word other than plain characters is
// quoted: a server name comes from a worker's needs.
func (p Problem) Command() string {
	argv := p.LoginArgv()
	for i, a := range argv {
		if a == "" || !plainWord.MatchString(a) {
			argv[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(argv, " ")
}

// LoginResult says how a login ended.
type LoginResult int

const (
	// LoggedIn means the command succeeded.
	LoggedIn LoginResult = iota
	// NeedsTerminal means the command needs a terminal that duolog lacks.
	NeedsTerminal
	// Declined covers a closed, refused or failed login.
	Declined
	// TimedOut means the person did not finish within the timeout, perhaps
	// because no browser opened; it may work from a terminal.
	TimedOut
)

// Checker is what delegate needs; tests replace it.
type Checker interface {
	// Agent reports a worker agent that is not logged in.
	Agent(ctx context.Context, agent string) *Problem
	// Server reports a server the worker cannot use. def is its definition
	// from a config file, or nil for a server no file lists.
	Server(ctx context.Context, agent, name string, def *capability.Server) *Problem
	// Login runs the agent's login for p once, within timeout.
	Login(ctx context.Context, p Problem, timeout time.Duration) LoginResult
	// Forget drops a passed check of p, so that the next check asks again.
	Forget(p Problem)
}

// New returns the real checker. cacheDir is .duolog/cache, or empty for none.
func New(cacheDir string) Checker {
	// A redirect would take the credential headers to another url, so the
	// check reads the first answer only.
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &checker{dir: cacheDir, client: client, run: output, now: time.Now}
}

type checker struct {
	dir    string
	client *http.Client
	run    func(ctx context.Context, argv []string) ([]byte, error)
	now    func() time.Time
	mu     sync.Mutex
}

func output(ctx context.Context, argv []string) ([]byte, error) {
	// The lead waits for this; a check that hangs passes rather than blocks.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader("")
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

func (c *checker) Agent(ctx context.Context, agent string) *Problem {
	p := Problem{Agent: agent}
	if c.passed(p) {
		return nil
	}
	var out []byte
	var err error
	switch agent {
	case "codex":
		// Exits 0 only when logged in.
		out, err = c.run(ctx, []string{"codex", "login", "status"})
		if err != nil {
			// No answer, a missing binary or a killed check is not a failed login.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() <= 0 {
				return nil
			}
			p.Reason = "codex login status says it is not logged in"
			return &p
		}
	case "claude":
		out, err = c.run(ctx, []string{"claude", "auth", "status", "--json"})
		var status struct {
			LoggedIn *bool `json:"loggedIn"`
		}
		if json.Unmarshal(out, &status) != nil || status.LoggedIn == nil {
			return nil
		}
		if !*status.LoggedIn {
			p.Reason = "claude auth status says it is not logged in"
			return &p
		}
	default:
		return nil
	}
	c.pass(p)
	return nil
}

func (c *checker) Server(ctx context.Context, agent, name string, def *capability.Server) *Problem {
	p := Problem{Agent: agent, Server: name}
	if c.passed(p) {
		return nil
	}
	var problem *Problem
	switch {
	case def != nil && def.Transport == "http" && def.Static():
		problem = c.probe(ctx, p, *def)
	case def != nil && def.Transport != "http":
		// A stdio server would have to be started to check it.
		return nil
	case agent == "codex":
		problem = c.codexOAuth(ctx, p)
	case agent == "claude":
		problem = c.claudeStatus(ctx, p)
	}
	if problem == nil {
		c.pass(p)
	}
	return problem
}

// initialize is the first MCP request. A server that checks its token at all
// checks it here; one that checks only on a tool call passes, and a rejection
// during the run reaches the lead through the worker's needs.
const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"duolog-preflight","version":"1"}}}`

var authWords = regexp.MustCompile(`(?i)unauthori[sz]ed|invalid[ _-]?token|expired[ _-]?token|token[ _-]?expired|forbidden|authentication (failed|required)`)

// probe sends initialize with the credentials the worker gets. Only 401, 403
// or an error that names authentication count; a network failure does not
// stop the worker.
func (c *checker) probe(ctx context.Context, p Problem, def capability.Server) *Problem {
	u, headers, err := def.Request()
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(initialize))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		end, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
		if err == nil {
			for k, v := range headers {
				end.Header.Set(k, v)
			}
			end.Header.Set("Mcp-Session-Id", session)
			if r, err := c.client.Do(end); err == nil {
				r.Body.Close()
			}
		}
	}
	rejected := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
	if !rejected && bytes.Contains(body, []byte(`"error"`)) && authWords.Match(body) {
		rejected = true
	}
	if !rejected {
		return nil
	}
	p.Static, p.Where = true, def.File
	p.Reason = fmt.Sprintf("the server rejected the token from config (HTTP %d)", resp.StatusCode)
	return &p
}

// codexOAuth reads the login state that `codex mcp list --json` reports.
func (c *checker) codexOAuth(ctx context.Context, p Problem) *Problem {
	out, err := c.run(ctx, []string{"codex", "mcp", "list", "--json"})
	var list []struct {
		Name       string `json:"name"`
		AuthStatus string `json:"auth_status"`
	}
	if err != nil || json.Unmarshal(out, &list) != nil {
		return nil
	}
	for _, s := range list {
		if s.Name == p.Server && s.AuthStatus == "not_logged_in" {
			p.Reason = "codex has no OAuth login for it"
			return &p
		}
	}
	return nil
}

// claudeStatus asks `claude mcp get`, which connects to the server without a
// model call and says when it needs authentication.
func (c *checker) claudeStatus(ctx context.Context, p Problem) *Problem {
	out, err := c.run(ctx, []string{"claude", "mcp", "get", p.Server})
	if err != nil {
		return nil
	}
	if strings.Contains(string(out), "Needs authentication") {
		p.Reason = "claude has no login for it"
		return &p
	}
	return nil
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

var terminalWords = regexp.MustCompile(`(?i)\btty\b|terminal|raw mode|interactive`)

func (c *checker) Login(ctx context.Context, p Problem, timeout time.Duration) LoginResult {
	argv := p.LoginArgv()
	if argv == nil {
		return Declined
	}
	if timeout <= 0 {
		timeout = DefaultLoginTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// A login may start a helper, such as a browser bridge; a timeout ends it too.
	runner.ProcessTree(cmd)
	cmd.Stdin = strings.NewReader("")
	// The output may hold a sign-in url; it is read for the terminal check only.
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	switch {
	case err == nil:
		c.Forget(p)
		return LoggedIn
	case ctx.Err() == nil && terminalWords.Match(out.Bytes()):
		return NeedsTerminal
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return TimedOut
	}
	return Declined
}

// The cache file maps a problem key to the time its check passed.
func (c *checker) path() string { return filepath.Join(c.dir, "auth.json") }

func (c *checker) load() map[string]time.Time {
	m := map[string]time.Time{}
	if c.dir == "" {
		return m
	}
	data, err := os.ReadFile(c.path())
	if err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func (c *checker) save(m map[string]time.Time) {
	if c.dir == "" || os.MkdirAll(c.dir, 0755) != nil {
		return
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	f, err := os.CreateTemp(c.dir, ".auth-*")
	if err != nil {
		return
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil && cerr == nil {
		_ = store.Rename(f.Name(), c.path())
	}
	_ = os.Remove(f.Name())
}

func (c *checker) passed(p Problem) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.load()[p.Key()]
	return ok && c.now().Sub(at) < cacheTTL && !at.After(c.now())
}

func (c *checker) pass(p Problem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.load()
	m[p.Key()] = c.now()
	c.save(m)
}

func (c *checker) Forget(p Problem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.load()
	delete(m, p.Key())
	c.save(m)
}

// FromNeeds turns the auth: entries a worker put in needs into problems.
// A worker writes auth:mcp:<server> for a server that rejected its
// credentials, or auth:<agent>.
func FromNeeds(agent string, needs []string) []Problem {
	var out []Problem
	for _, need := range needs {
		rest, ok := strings.CutPrefix(strings.TrimSpace(need), "auth:")
		if !ok {
			continue
		}
		if server, ok := strings.CutPrefix(rest, "mcp:"); ok && server != "" {
			out = append(out, Problem{Agent: agent, Server: server, Reason: "the worker says the server rejected its credentials"})
		} else {
			out = append(out, Problem{Agent: agent, Reason: "the worker says its login failed"})
		}
	}
	return out
}

// WorkerLine tells a worker with MCP needs how to report a rejected login.
const WorkerLine = "If an MCP server rejects its credentials (401, 403, an invalid or expired token, or a login it needs), stop and answer done false with auth:mcp:<server> in needs."
