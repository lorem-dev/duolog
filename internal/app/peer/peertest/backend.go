package peertest

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	corepane "github.com/lorem-dev/duolog/internal/core/pane"
)

// Pane is the port under test. It repeats the method set of peer.Pane and
// peer.PaneCloser, which a test of package peer cannot import from here
// without a cycle.
type Pane interface {
	Open(ctx context.Context, o corepane.Open) (corepane.ID, error)
	Send(ctx context.Context, id corepane.ID, text string) error
	Key(ctx context.Context, id corepane.ID, key string) error
	Capture(ctx context.Context, id corepane.ID, lines int) ([]byte, error)
	CaptureStyled(ctx context.Context, id corepane.ID, lines int) ([]byte, error)
	Probe(ctx context.Context, id corepane.ID) (corepane.Presence, error)
	Lead() (string, bool)
	Sessions(ctx context.Context) ([]corepane.Session, error)
	RenameSession(ctx context.Context, session, label string) error
	CloseSession(ctx context.Context, session string) error
	Clients(ctx context.Context) ([]corepane.Client, error)
	Gone(err error) bool
	Refused(err error) bool
	Close(ctx context.Context, id corepane.ID) error
	Focus(ctx context.Context, id corepane.ID, client string) error
}

var (
	// ErrGone is the error of a pane or session that is not there.
	ErrGone = errors.New("pane not found")
	// ErrRefused is the error of a call that the backend refused or that
	// never reached it.
	ErrRefused = errors.New("pane backend refused")
)

// Reply is the scripted answer to one call.
type Reply struct {
	// Block and Session are where an open put the pane. An open of a tab
	// keeps its session when Session is empty.
	Block, Session string
	// Text is the screen a capture reads, before the fake keeps the last lines.
	Text     string
	Presence corepane.Presence
	Sessions []corepane.Session
	Clients  []corepane.Client
	Err      error
	// Sequence replaces the reply by call order: the nth call that selects
	// this reply uses Sequence[n], and the last entry repeats.
	Sequence []Reply
}

// FakePane is a Pane that records its calls as words and answers each from
// Script. A call selects the reply named "method block" when the script has
// one, else the reply named by its method: open-session, open-tab, probe,
// capture (both kinds), send, key, close, focus, sessions, rename,
// close-session or clients. A call with no reply fails.
type FakePane struct {
	// LeadSession is what Lead reports; empty means no backend.
	LeadSession string
	Script      map[string]Reply

	mu    sync.Mutex
	calls [][]string
	seen  map[string]int
}

// Calls lists the calls so far, each as its method and arguments: open-session
// label title dir -- argv; open-tab session title dir -- argv; send session
// block text; key session block key; focus session block client; rename
// session label; close-session session; sessions; clients; and method session
// block for the rest.
func (f *FakePane) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Reset forgets the calls and restarts every sequence, with a new script.
func (f *FakePane) Reset(script map[string]Reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Script, f.calls, f.seen = script, nil, nil
}

// call records words and returns the reply that method, or method block,
// selects.
func (f *FakePane) call(method, block string, words ...string) Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{method}, words...))
	key := method
	if _, ok := f.Script[method+" "+block]; block != "" && ok {
		key = method + " " + block
	}
	r, ok := f.Script[key]
	if !ok {
		return Reply{Err: fmt.Errorf("unexpected pane call %s", method)}
	}
	if len(r.Sequence) > 0 {
		if f.seen == nil {
			f.seen = map[string]int{}
		}
		n := min(f.seen[key], len(r.Sequence)-1)
		f.seen[key]++
		r = r.Sequence[n]
	}
	return r
}

func (f *FakePane) Lead() (string, bool) { return f.LeadSession, f.LeadSession != "" }

func (f *FakePane) Open(_ context.Context, o corepane.Open) (corepane.ID, error) {
	if len(o.Argv) == 0 {
		return corepane.ID{}, errors.New("pane open: missing command")
	}
	method, where := "open-session", o.SessionLabel
	if o.Session != "" {
		method, where = "open-tab", o.Session
	}
	r := f.call(method, "", append([]string{where, o.Title, o.Dir, "--"}, o.Argv...)...)
	if r.Err != nil {
		return corepane.ID{}, r.Err
	}
	id := corepane.ID{Session: r.Session, Block: r.Block}
	if o.Session != "" && id.Session == "" {
		id.Session = o.Session
	}
	return id, nil
}

func (f *FakePane) Send(_ context.Context, id corepane.ID, text string) error {
	return f.call("send", id.Block, id.Session, id.Block, text).Err
}

func (f *FakePane) Key(_ context.Context, id corepane.ID, key string) error {
	return f.call("key", id.Block, id.Session, id.Block, key).Err
}

func (f *FakePane) Capture(_ context.Context, id corepane.ID, lines int) ([]byte, error) {
	r := f.call("capture", id.Block, id.Session, id.Block)
	if r.Err != nil {
		return nil, r.Err
	}
	return lastLines(r.Text, lines, false), nil
}

func (f *FakePane) CaptureStyled(_ context.Context, id corepane.ID, lines int) ([]byte, error) {
	r := f.call("capture", id.Block, id.Session, id.Block)
	if r.Err != nil {
		return nil, r.Err
	}
	return lastLines(r.Text, lines, true), nil
}

func (f *FakePane) Probe(_ context.Context, id corepane.ID) (corepane.Presence, error) {
	r := f.call("probe", id.Block, id.Session, id.Block)
	if r.Err != nil {
		return corepane.Gone, r.Err
	}
	return r.Presence, nil
}

// Close is not an error for a pane that is gone.
func (f *FakePane) Close(_ context.Context, id corepane.ID) error {
	err := f.call("close", id.Block, id.Session, id.Block).Err
	if errors.Is(err, ErrGone) {
		return nil
	}
	return err
}

func (f *FakePane) Focus(_ context.Context, id corepane.ID, client string) error {
	return f.call("focus", id.Block, id.Session, id.Block, client).Err
}

func (f *FakePane) Sessions(context.Context) ([]corepane.Session, error) {
	r := f.call("sessions", "")
	return r.Sessions, r.Err
}

func (f *FakePane) RenameSession(_ context.Context, session, label string) error {
	return f.call("rename", "", session, label).Err
}

// CloseSession is not an error for a session that is gone.
func (f *FakePane) CloseSession(_ context.Context, session string) error {
	err := f.call("close-session", "", session).Err
	if errors.Is(err, ErrGone) {
		return nil
	}
	return err
}

func (f *FakePane) Clients(context.Context) ([]corepane.Client, error) {
	r := f.call("clients", "")
	return r.Clients, r.Err
}

func (*FakePane) Gone(err error) bool { return errors.Is(err, ErrGone) }

func (*FakePane) Refused(err error) bool {
	return errors.Is(err, ErrRefused) || errors.Is(err, ErrGone)
}

// escapes matches the escape sequences of a terminal capture; sgr matches the
// one that sets styles.
var (
	escapes = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[\x20-\x2f]*[\x30-\x7e])`)
	sgr     = regexp.MustCompile(`^\x1b\[[0-9;:]*m$`)
)

// lastLines keeps the last n lines that show text, with no trailing spaces:
// n <= 0 keeps 20. Plain lines lose every escape sequence, styled ones every
// sequence but the styles.
func lastLines(text string, n int, styled bool) []byte {
	if n <= 0 {
		n = 20
	}
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(escapes.ReplaceAllString(line, "")) == "" {
			continue
		}
		line = escapes.ReplaceAllStringFunc(line, func(seq string) string {
			if styled && sgr.MatchString(seq) {
				return seq
			}
			return ""
		})
		kept = append(kept, strings.TrimRight(line, " \t\r"))
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return []byte(strings.Join(kept, "\n"))
}

// ContractScript is the world PaneContract expects of a FakePane, with its
// lead in session "sess".
func ContractScript() map[string]Reply {
	return map[string]Reply{
		"open-session":          {Session: "session:new", Block: "block:new"},
		"open-tab":              {Block: "block:tab"},
		"probe":                 {Presence: corepane.Running},
		"probe block:missing":   {Presence: corepane.Gone},
		"capture":               {Text: "a\n\nb\nc\n"},
		"capture block:missing": {Err: fmt.Errorf("capture block:missing: %w", ErrGone)},
		"send":                  {},
		"key":                   {},
		"close":                 {},
		"close block:missing":   {Err: ErrGone},
		"focus":                 {},
		"sessions":              {Sessions: []corepane.Session{{ID: "sess", Label: "lead"}}},
		"rename":                {},
		"close-session":         {},
		"clients":               {Clients: []corepane.Client{{ID: "client:app", Kind: "app", Sessions: []string{"sess"}}}},
	}
}

// PaneContract runs the cases every Pane meets. newPane gives a backend in
// this world: the lead's pane is in a session; a new session and a tab in any
// session open; the block "block:missing" of any session is gone and every
// other block runs and shows the lines a, b and c, with blank lines between;
// the session list holds the lead's session; and one app client shows the
// lead's session.
func PaneContract(t *testing.T, newPane func(t *testing.T) Pane) {
	t.Helper()
	for _, c := range paneCases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newPane(t)) })
	}
}

var paneCases = []struct {
	name string
	run  func(t *testing.T, p Pane)
}{
	{"lead names a session", leadNamesASession},
	{"open makes a new session", openMakesANewSession},
	{"open joins a session", openJoinsASession},
	{"open needs a command", openNeedsACommand},
	{"a running pane", aRunningPane},
	{"a pane that is gone", aPaneThatIsGone},
	{"errors of other kinds", errorsOfOtherKinds},
	{"sessions", sessionsListRenameClose},
	{"clients", clientsShowTheLead},
}

var contractArgv = []string{"agent-bin", "--flag"}

// openNew opens a pane in a new session.
func openNew(t *testing.T, p Pane) corepane.ID {
	t.Helper()
	id, err := p.Open(context.Background(), corepane.Open{Argv: contractArgv, Title: "agent-role", Dir: t.TempDir(), SessionLabel: "duolog | project"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func leadNamesASession(t *testing.T, p Pane) {
	if session, ok := p.Lead(); !ok || session == "" {
		t.Fatalf("Lead() = %q, %v", session, ok)
	}
}

func openMakesANewSession(t *testing.T, p Pane) {
	lead, _ := p.Lead()
	id := openNew(t, p)
	if id.Session == "" || id.Session == lead || id.Block == "" {
		t.Fatalf("new session pane %+v beside lead %q", id, lead)
	}
}

func openJoinsASession(t *testing.T, p Pane) {
	id, err := p.Open(context.Background(), corepane.Open{Argv: contractArgv, Title: "agent-role", Dir: t.TempDir(), Session: "session:peers"})
	if err != nil || id.Session != "session:peers" || id.Block == "" {
		t.Fatalf("tab %+v, %v", id, err)
	}
}

func openNeedsACommand(t *testing.T, p Pane) {
	if id, err := p.Open(context.Background(), corepane.Open{Title: "agent-role", SessionLabel: "duolog | project"}); err == nil {
		t.Fatalf("opened %+v with no argv", id)
	}
}

func aRunningPane(t *testing.T, p Pane) {
	ctx := context.Background()
	id := openNew(t, p)
	if presence, err := p.Probe(ctx, id); err != nil || presence != corepane.Running {
		t.Fatalf("Probe = %v, %v", presence, err)
	}
	if err := errors.Join(p.Send(ctx, id, "text"), p.Key(ctx, id, "enter")); err != nil {
		t.Fatal(err)
	}
	for _, capture := range []func(context.Context, corepane.ID, int) ([]byte, error){p.Capture, p.CaptureStyled} {
		if got, err := capture(ctx, id, 2); err != nil || string(got) != "b\nc" {
			t.Fatalf("capture of 2 lines = %q, %v", got, err)
		}
	}
	if err := errors.Join(p.Focus(ctx, id, "client:app"), p.Close(ctx, id)); err != nil {
		t.Fatal(err)
	}
}

func aPaneThatIsGone(t *testing.T, p Pane) {
	ctx := context.Background()
	id := corepane.ID{Session: openNew(t, p).Session, Block: "block:missing"}
	if presence, err := p.Probe(ctx, id); err != nil || presence != corepane.Gone {
		t.Fatalf("Probe = %v, %v", presence, err)
	}
	if _, err := p.Capture(ctx, id, 2); !p.Gone(err) || !p.Refused(err) {
		t.Fatalf("capture of a gone pane: %v, Gone %v, Refused %v", err, p.Gone(err), p.Refused(err))
	}
	if err := p.Close(ctx, id); err != nil {
		t.Fatalf("close of a gone pane: %v", err)
	}
}

func errorsOfOtherKinds(t *testing.T, p Pane) {
	for _, err := range []error{nil, errors.New("plain"), context.Canceled} {
		if p.Gone(err) || p.Refused(err) {
			t.Fatalf("%v counts as Gone %v or Refused %v", err, p.Gone(err), p.Refused(err))
		}
	}
}

func sessionsListRenameClose(t *testing.T, p Pane) {
	ctx := context.Background()
	lead, _ := p.Lead()
	sessions, err := p.Sessions(ctx)
	if err != nil || !slices.ContainsFunc(sessions, func(s corepane.Session) bool { return s.ID == lead }) {
		t.Fatalf("Sessions = %+v, %v; lead %q", sessions, err, lead)
	}
	id := openNew(t, p)
	if err := errors.Join(p.RenameSession(ctx, id.Session, "duolog | project (1)"), p.CloseSession(ctx, id.Session)); err != nil {
		t.Fatal(err)
	}
}

func clientsShowTheLead(t *testing.T, p Pane) {
	lead, _ := p.Lead()
	clients, err := p.Clients(context.Background())
	if err != nil || !slices.ContainsFunc(clients, func(c corepane.Client) bool { return c.Kind == "app" && slices.Contains(c.Sessions, lead) }) {
		t.Fatalf("Clients = %+v, %v; lead %q", clients, err, lead)
	}
}
