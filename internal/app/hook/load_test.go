package hook

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/session"
)

const wantLoad = "duolog peers: claude 3 running (1 idle) of 12; 5h 41%; idle: peer-ab12cd"

// withLoad gives e a Load that counts its calls and answers for the lead.
func withLoad(e Env, calls *int, seen *string) Env {
	e.Load = func(_ context.Context, leadID string) load.Summary {
		*calls++
		*seen = leadID
		return load.Summary{
			Agents: []load.Agent{{Name: "claude", Running: 3, Idle: 1, Limit: 12, Live: 7}},
			Idle:   []string{"peer-ab12cd"}, Window: 41, WindowKnown: true,
		}
	}
	return e
}

func registered(t *testing.T, agent string) (Env, *bus.Bus) {
	t.Helper()
	e, _, b := setup(t, agent)
	SessionStart(context.Background(), e)
	return e, b
}

func tellLead(t *testing.T, b *bus.Bus, e Env) {
	t.Helper()
	to := session.Address(session.Lead{ID: id(e), Target: config.Target{Agent: e.Agent}})
	if _, err := b.Send(bus.Message{From: "codex/review", To: to, K: "done", S: "ready"}); err != nil {
		t.Fatal(err)
	}
}

func TestPromptAddsTheLoadLineForARegisteredLead(t *testing.T) {
	e, _ := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	got := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	if !strings.Contains(got, wantLoad) {
		t.Fatalf("got %q, want the line %q", got, wantLoad)
	}
	if seen != id(e) {
		t.Fatalf("Load asked for lead %q, want %q", seen, id(e))
	}
	// Every prompt carries it, also an unchanged one.
	if got := content(t, Prompt(context.Background(), e), "UserPromptSubmit"); !strings.Contains(got, wantLoad) {
		t.Fatalf("second prompt: %q", got)
	}
}

func TestPromptLoadLineAdvisesFreeSessionsOfAnEmptyQueue(t *testing.T) {
	e, _ := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	queued := 0
	e.Load = func(context.Context, string) load.Summary {
		return load.Summary{Agents: []load.Agent{{Name: "claude", Running: 3, Limit: 12, Live: 3}}, Queued: queued}
	}
	got := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	if !strings.Contains(got, " - 9 claude sessions free: start independent work") {
		t.Fatalf("got %q", got)
	}
	queued = 2
	got = content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	if strings.Contains(got, "start independent work") || !strings.Contains(got, "queue 2 - 9 claude sessions free: duolog peer queue run") {
		t.Fatalf("queued items take the free sessions: %q", got)
	}
}

// The lead's prompt starts its queue run; the run itself checks the queue and
// the free sessions. Workers, peers and a session that is no lead start none.
func TestPromptStartsTheLeadsQueueRun(t *testing.T) {
	e, _ := registered(t, "claude")
	var leads []string
	e.QueueRun = func(_ context.Context, leadID string) { leads = append(leads, leadID) }
	silent(t, Prompt(context.Background(), e))
	if len(leads) != 1 || leads[0] != id(e) {
		t.Fatalf("QueueRun calls %v, want one for %q", leads, id(e))
	}
	silent(t, PostTool(context.Background(), e))
	if len(leads) != 1 {
		t.Fatalf("a tool call without a peer-idle message started a run: %v", leads)
	}
	for _, extra := range []string{"DUOLOG_DEPTH=1", "DUOLOG_PEER=peer-ab12cd"} {
		other := e
		other.Environ = append(slices.Clone(e.Environ), extra)
		Prompt(context.Background(), other)
	}
	stranger, _, _ := setup(t, "claude")
	stranger.QueueRun = e.QueueRun
	Prompt(context.Background(), stranger)
	if len(leads) != 1 {
		t.Fatalf("QueueRun calls %v, want none for a worker, a peer or no lead", leads)
	}
}

// A peer-idle message that reaches the lead calls PeerIdle once, with the lead
// and the peer; other messages do not.
func TestLeadDeliveryOfPeerIdleCallsPeerIdle(t *testing.T) {
	e, b := registered(t, "claude")
	var calls []string
	e.PeerIdle = func(_ context.Context, leadID, peerID string) { calls = append(calls, leadID+" "+peerID) }
	tellLead(t, b, e)
	content(t, PostTool(context.Background(), e), "PostToolUse")
	if len(calls) != 0 {
		t.Fatalf("PeerIdle calls %v for a done message", calls)
	}
	to := session.Address(session.Lead{ID: id(e), Target: config.Target{Agent: e.Agent}})
	if _, err := b.Send(bus.Message{From: "codex/implement", To: to, K: "peer-idle", Ref: "peer-ab12cd", S: "peer-ab12cd idle in ."}); err != nil {
		t.Fatal(err)
	}
	if got := content(t, PostTool(context.Background(), e), "PostToolUse"); !strings.Contains(got, "peer-idle peer-ab12cd") {
		t.Fatalf("got %q", got)
	}
	if want := id(e) + " peer-ab12cd"; len(calls) != 1 || calls[0] != want {
		t.Fatalf("PeerIdle calls %v, want %q once", calls, want)
	}
	silent(t, PostTool(context.Background(), e))
	if len(calls) != 1 {
		t.Fatalf("a delivered peer-idle called PeerIdle again: %v", calls)
	}
}

func TestPromptWithoutLoadOrPeersAddsNothing(t *testing.T) {
	e, _ := registered(t, "claude")
	silent(t, Prompt(context.Background(), e))
	e.Load = func(context.Context, string) load.Summary { return load.Summary{} }
	silent(t, Prompt(context.Background(), e))
}

func TestPromptLoadLineNeedsARegisteredLead(t *testing.T) {
	e, _, _ := setup(t, "claude") // never started: a Claude prompt does not register
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	silent(t, Prompt(context.Background(), e))
	if calls != 0 {
		t.Fatalf("Load ran %d times for a session that is no lead", calls)
	}
}

func TestWorkersGetNoLoadLine(t *testing.T) {
	e, _ := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	silent(t, Prompt(context.Background(), e))
	silent(t, PostTool(context.Background(), e))
	if calls != 0 {
		t.Fatalf("Load ran %d times for a worker", calls)
	}
}

func TestPeersGetNoLoadLine(t *testing.T) {
	e, _ := registered(t, "codex")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	e.Environ = append(e.Environ, "DUOLOG_PEER=peer-ab12cd")
	silent(t, Prompt(context.Background(), e))
	if calls != 0 {
		t.Fatalf("Load ran %d times for a peer", calls)
	}
}

func TestPostToolCarriesTheLoadLineWithABusDelivery(t *testing.T) {
	e, b := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	// No bus message: nothing to ride along with.
	silent(t, PostTool(context.Background(), e))

	tellLead(t, b, e)
	got := content(t, PostTool(context.Background(), e), "PostToolUse")
	if !strings.Contains(got, "ready") || !strings.Contains(got, wantLoad) {
		t.Fatalf("got %q", got)
	}
	// Within five minutes and unchanged, the next delivery has no line.
	tellLead(t, b, e)
	got = content(t, PostTool(context.Background(), e), "PostToolUse")
	if !strings.Contains(got, "ready") || strings.Contains(got, "duolog peers:") {
		t.Fatalf("an unchanged line came again: %q", got)
	}
}

func TestPostToolLoadLineComesAgainWhenANumberChanges(t *testing.T) {
	e, b := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	tellLead(t, b, e)
	content(t, PostTool(context.Background(), e), "PostToolUse")

	e.Load = func(context.Context, string) load.Summary {
		return load.Summary{Agents: []load.Agent{{Name: "claude", Running: 4, Limit: 12, Live: 8}}}
	}
	tellLead(t, b, e)
	if got := content(t, PostTool(context.Background(), e), "PostToolUse"); !strings.Contains(got, "claude 4 running of 12") {
		t.Fatalf("got %q", got)
	}
}

func TestPostToolLoadLineFollowsAPromptsLine(t *testing.T) {
	e, b := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	tellLead(t, b, e)
	if got := content(t, PostTool(context.Background(), e), "PostToolUse"); strings.Contains(got, "duolog peers:") {
		t.Fatalf("the prompt just showed this line: %q", got)
	}
}

func TestPostToolLoadLineComesAgainAfterFiveMinutes(t *testing.T) {
	e, b := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	tellLead(t, b, e)
	content(t, PostTool(context.Background(), e), "PostToolUse")
	// The state folder remembers the line; age it.
	loadMark(filepath.Join(e.Root, ".duolog"), id(e), wantLoad, time.Now().Add(-6*time.Minute))
	tellLead(t, b, e)
	if got := content(t, PostTool(context.Background(), e), "PostToolUse"); !strings.Contains(got, wantLoad) {
		t.Fatalf("got %q", got)
	}
}
