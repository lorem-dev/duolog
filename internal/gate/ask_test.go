package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
)

func askSetup(t *testing.T) (*bus.Bus, *session.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []session.Lead{
		{ID: "first-123456", Target: config.Target{Agent: "codex"}, Mode: mode.Ask},
		{ID: "other-123456", Target: config.Target{Agent: "claude"}, Mode: mode.Ask},
	} {
		if err := r.RegisterLead(l); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DUOLOG_LEAD", "first-123456")
	b, err := bus.Open(dir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	return b, r, dir
}
func askMessages(t *testing.T, dir string) []bus.Message {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var messages []bus.Message
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var m bus.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
	}
	return messages
}
func askReply(t *testing.T, kind, from string, unknown bool, want string) {
	t.Helper()
	b, _, dir := askSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan Decision, 1)
	go func() {
		result <- Escalate(ctx, b, "first-123456", Action{Kind: "exec", Target: "custom-command", Role: "implement"}, 1100*time.Millisecond, false)
	}()
	var request bus.Message
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ms := askMessages(t, dir)
		if len(ms) > 0 {
			request = ms[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if request.N == 0 {
		t.Fatal("no ask sent")
	}
	ref := request.N
	if unknown {
		ref += 99
	}
	if _, err := b.Send(bus.Message{From: from, To: request.From, K: kind, Ref: strconv.FormatInt(ref, 10), S: kind}); err != nil {
		t.Fatal(err)
	}
	d := <-result
	if d.Kind != want || d.Reason == "" || d.Rule == "" {
		t.Fatalf("decision: %+v", d)
	}
	if want == Deny && kind == Allow && !strings.Contains(d.Reason, "timeout") {
		t.Fatalf("invalid reply was accepted: %+v", d)
	}
}

// askSendWait outlasts a slow runner's setup: Escalate sends nothing once its
// deadline has passed, and these tests read the ask it sent.
const askSendWait = time.Second

func TestAskSendsOneLine(t *testing.T) {
	for _, person := range []bool{false, true} {
		t.Run(strconv.FormatBool(person), func(t *testing.T) {
			b, _, dir := askSetup(t)
			d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: strings.Repeat("界\n\"", 400), Role: "implement"}, askSendWait, person)
			ms := askMessages(t, dir)
			if d.Kind != Deny || len(ms) != 1 {
				t.Fatal(d, ms)
			}
			data, err := os.ReadFile(filepath.Join(dir, "bus.ndjson"))
			if err != nil {
				t.Fatal(err)
			}
			if len(data) > 512 || strings.Count(string(data), "\n") != 1 || len(ms[0].S) > 200 || !utf8.ValidString(ms[0].S) || strings.ContainsAny(ms[0].S, "\r\n") || ms[0].K != "ask" {
				t.Fatalf("bad ask: %s", data)
			}
			if ms[0].P != person || strings.Contains(string(data), `"p":true`) != person || !person && strings.Contains(string(data), `"p":`) {
				t.Fatalf("bad person flag: %s", data)
			}
		})
	}
}

func TestOutsideAskIsPersonOnly(t *testing.T) {
	p := policy(t)
	d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "read", Target: "/elsewhere/notes.txt", Role: "implement"})
	if !PersonOnly(d) {
		t.Fatal(d)
	}
	d = Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "net", Target: "https://example.com", Role: "implement"})
	if PersonOnly(d) {
		t.Fatal("a web ask stays with the lead:", d)
	}
	d = Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: "cat /elsewhere/notes.txt", Role: "implement"})
	if !PersonOnly(d) {
		t.Fatal("a read command outside the work root needs the person:", d)
	}
	d = Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "write", Target: "/elsewhere/notes.txt", Role: "implement"})
	if !PersonOnly(d) {
		t.Fatal("a write outside the work root needs the person:", d)
	}
}

func TestPersonOnlyReadCommandsKeepOutsideRule(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		"cat *.go; cat /elsewhere/notes.txt",
		"cat $(pwd)/notes.txt; cat /elsewhere/notes.txt",
	} {
		d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement"})
		if !PersonOnly(d) {
			t.Errorf("%q: %+v", command, d)
		}
	}
}

func TestPersonOnlyAskCannotBypassRoleOrPlanDeny(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		name, command, role, rule string
		lead                      LeadRules
		m                         mode.Mode
	}{
		{"role-sandbox", "cat $(pwd)/x; cat /elsewhere/n", "implement", "role sandbox", LeadRules{RoleSandbox: "read-only"}, mode.Edits},
		{"read-only-role", "cat $(pwd)/x; cat /elsewhere/n", "review_final", "role sandbox", LeadRules{}, mode.Full},
		{"plan-uncertain", "touch f; cat $(x) /elsewhere/n", "implement", "mode plan", LeadRules{}, mode.Plan},
		{"plan-write", "touch f; cat /elsewhere/n", "implement", "mode plan", LeadRules{}, mode.Plan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Evaluate(p, tc.lead, tc.m, Action{Kind: "exec", Target: tc.command, Role: tc.role})
			if d.Kind != Deny || d.Rule != tc.rule || PersonOnly(d) {
				t.Fatal(d)
			}
		})
	}
}

func TestExplicitOutsideApprovalIsNotPersonOnly(t *testing.T) {
	p := policy(t)
	p.Rules = append([]config.Rule{{Action: "read", Match: "/elsewhere/**", Decision: Allow}}, p.Rules...)
	d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "read", Target: "/elsewhere/notes.txt", Role: "implement"})
	if d.Kind != Allow || PersonOnly(d) {
		t.Fatal(d)
	}
	d = Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "read", Target: "/elsewhere/secret.key", Role: "implement"})
	if d.Kind != Deny || PersonOnly(d) {
		t.Fatal(d)
	}
}
func TestAllowAccepted(t *testing.T) { askReply(t, Allow, "codex/lead.first-", false, Allow) }
func TestDenyAccepted(t *testing.T)  { askReply(t, Deny, "codex/lead.first-", false, Deny) }
func TestAllowFromOtherLeadIgnored(t *testing.T) {
	askReply(t, Allow, "claude/lead.other-", false, Deny)
}
func TestAllowFromWorkerIgnored(t *testing.T) { askReply(t, Allow, "codex/worker", false, Deny) }
func TestAllowForUnknownRequestIgnored(t *testing.T) {
	askReply(t, Allow, "codex/lead.first-", true, Deny)
}
func TestTimeoutDenies(t *testing.T) {
	b, _, _ := askSetup(t)
	start := time.Now()
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, 30*time.Millisecond, false)
	if d.Kind != Deny || !strings.Contains(d.Reason, "timeout") || time.Since(start) > time.Second {
		t.Fatal(d)
	}
}
func TestAskGoesToSpawningLead(t *testing.T) {
	b, _, dir := askSetup(t)
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, askSendWait, false)
	ms := askMessages(t, dir)
	if len(ms) != 1 || ms[0].To != "codex/lead.first-" {
		t.Fatal(ms)
	}
}

// The ask names the peer pane whose worker sent it, so that waiting on another
// peer of the same lead does not stop on it.
func TestAskNamesItsPeer(t *testing.T) {
	b, _, dir := askSetup(t)
	t.Setenv("DUOLOG_PEER", "peer-abc123")
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, askSendWait, false)
	ms := askMessages(t, dir)
	if len(ms) != 1 || ms[0].Ref != "peer-abc123" {
		t.Fatal(ms)
	}
	// A worker outside a peer pane sends an ask that names no peer.
	b, _, dir = askSetup(t)
	t.Setenv("DUOLOG_PEER", "")
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, askSendWait, false)
	if ms := askMessages(t, dir); len(ms) != 1 || ms[0].Ref != "" {
		t.Fatal(ms)
	}
}

// An ask carries how long its asker waits, so readers stop treating it as
// pending once the asker has given up, even with a short ask_timeout_seconds.
func TestAskCarriesItsWait(t *testing.T) {
	b, _, dir := askSetup(t)
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, 1500*time.Millisecond, false)
	if ms := askMessages(t, dir); len(ms) != 1 || ms[0].W != 2 {
		t.Fatal(ms)
	}
}

func TestAskExpiresWithItsAskersWait(t *testing.T) {
	sent := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format(time.RFC3339Nano) }
	for _, tc := range []struct {
		name    string
		m       bus.Message
		expired bool
	}{
		{"wait passed", bus.Message{T: sent(31 * time.Second), W: 30}, true},
		{"wait running", bus.Message{T: sent(29 * time.Second), W: 30}, false},
		// An ask from before asks carried their wait keeps the hook budget.
		{"no wait, within hook budget", bus.Message{T: sent(MaxAskWait)}, false},
		{"no wait, past hook budget", bus.Message{T: sent(MaxAskWait + 31*time.Second)}, true},
	} {
		if got := AskExpired(tc.m); got != tc.expired {
			t.Errorf("%s: expired %v, want %v", tc.name, got, tc.expired)
		}
	}
}

func TestAskToEndedLeadDeniesAtOnce(t *testing.T) {
	b, r, dir := askSetup(t)
	if err := r.Close("first-123456"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, time.Minute, false)
	if d.Kind != Deny || time.Since(start) > time.Second || len(askMessages(t, dir)) != 0 {
		t.Fatal(d)
	}
}
func TestNoLeadEnvDenies(t *testing.T) {
	b, _, dir := askSetup(t)
	t.Setenv("DUOLOG_LEAD", "")
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, time.Minute, false)
	if d.Kind != Deny || len(askMessages(t, dir)) != 0 {
		t.Fatal(d)
	}
}

func TestAskCancellationDenies(t *testing.T) {
	b, _, dir := askSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := Escalate(ctx, b, "first-123456", Action{Kind: "exec"}, time.Minute, false)
	if d.Kind != Deny || len(askMessages(t, dir)) != 0 {
		t.Fatal(d)
	}
}

func TestAskHonorsSmallerWireLimits(t *testing.T) {
	_, r, dir := askSetup(t)
	b, err := bus.Open(r.Dir, config.Wire{MaxLineBytes: 220, MaxSummaryBytes: 40})
	if err != nil {
		t.Fatal(err)
	}
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: strings.Repeat("\"", 500), Role: "implement"}, askSendWait, false)
	data, err := os.ReadFile(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	ms := askMessages(t, dir)
	if len(ms) != 1 || len(data) > 220 || len(ms[0].S) > 40 {
		t.Fatalf("bad ask: %s", data)
	}
}
