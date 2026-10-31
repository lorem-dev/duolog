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
		result <- Escalate(ctx, b, "first-123456", Action{Kind: "exec", Target: "custom-command", Role: "implement"}, 1100*time.Millisecond)
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
	b, _, dir := askSetup(t)
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: strings.Repeat("界\n\"", 400), Role: "implement"}, askSendWait)
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
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, 30*time.Millisecond)
	if d.Kind != Deny || !strings.Contains(d.Reason, "timeout") || time.Since(start) > time.Second {
		t.Fatal(d)
	}
}
func TestAskGoesToSpawningLead(t *testing.T) {
	b, _, dir := askSetup(t)
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, askSendWait)
	ms := askMessages(t, dir)
	if len(ms) != 1 || ms[0].To != "codex/lead.first-" {
		t.Fatal(ms)
	}
}
func TestAskToEndedLeadDeniesAtOnce(t *testing.T) {
	b, r, dir := askSetup(t)
	if err := r.Close("first-123456"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, time.Minute)
	if d.Kind != Deny || time.Since(start) > time.Second || len(askMessages(t, dir)) != 0 {
		t.Fatal(d)
	}
}
func TestNoLeadEnvDenies(t *testing.T) {
	b, _, dir := askSetup(t)
	t.Setenv("DUOLOG_LEAD", "")
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec"}, time.Minute)
	if d.Kind != Deny || len(askMessages(t, dir)) != 0 {
		t.Fatal(d)
	}
}

func TestAskCancellationDenies(t *testing.T) {
	b, _, dir := askSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := Escalate(ctx, b, "first-123456", Action{Kind: "exec"}, time.Minute)
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
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: strings.Repeat("\"", 500), Role: "implement"}, askSendWait)
	data, err := os.ReadFile(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	ms := askMessages(t, dir)
	if len(ms) != 1 || len(data) > 220 || len(ms[0].S) > 40 {
		t.Fatalf("bad ask: %s", data)
	}
}
