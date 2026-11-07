package peer

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	corepane "github.com/lorem-dev/duolog/internal/core/pane"
)

const (
	recordedApp = "client:01a00000-0000-7000-8000-000000000001"
	otherApp    = "client:01a00000-0000-7000-8000-000000000002"
)

// clientsOn is the clients of the recorded `rex client ls --json` reply: the
// first app shows session, the second app no session, and one command line
// client shows session as well.
func clientsOn(session string) []corepane.Client {
	return []corepane.Client{
		{ID: recordedApp, Kind: "app", Name: "Rex", Sessions: []string{session, "session:01a00000-0000-7000-8000-0000000000bb"}},
		{ID: otherApp, Kind: "app", Name: "Rex"},
		{ID: "client:01a00000-0000-7000-8000-000000000003", Kind: "cli", Name: "rex", Sessions: []string{session}},
	}
}

func viewScript(clients []corepane.Client) map[string]peertest.Reply {
	script := baseScript()
	script["clients"] = peertest.Reply{Clients: clients}
	script["focus"] = peertest.Reply{}
	return script
}

func sessionsFile(t *testing.T, r *rig) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.reg.Dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestViewFocusesExistingTab(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	t.Setenv("REX_BLOCK", "block:lead")
	r.panes(viewScript(clientsOn("sess")))
	before := sessionsFile(t, r)

	got, err := View(r.ctx, e.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	focus := r.named("focus")
	want := [][]string{{"focus", "session:peers", "block:peer", recordedApp}}
	if !slices.EqualFunc(focus, want, slices.Equal) {
		t.Fatalf("%v", focus)
	}
	if len(r.spawns()) != 0 || len(r.keys()) != 0 || len(r.named("send")) != 0 || len(r.named("close")) != 0 {
		t.Fatalf("view changed panes: %v", r.calls())
	}
	if sessionsFile(t, r) != before {
		t.Fatal("view wrote a record")
	}
	// The person is now in the peer's pane, where a typed command would go
	// to the peer, so the line names the Rex way back instead of a command.
	line := e.ID + " shown on client " + recordedApp + ": duolog | codex-implement; to return, use Previous Session in Rex"
	if got.Line() != line || strings.Count(got.Line(), "\n") != 0 || strings.Contains(got.Line(), "back:") {
		t.Fatalf("%q", got.Line())
	}
}

func TestViewRefusesAmbiguousClient(t *testing.T) {
	const refusal = "no single Rex client shows this session; run rex client ls and pass --client <id>"
	two := clientsOn("sess")
	two[1].Sessions = []string{"sess"}
	for name, clients := range map[string][]corepane.Client{
		"zero": clientsOn("session:elsewhere"),
		"two":  two,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, baseScript())
			e := r.ready()
			r.panes(viewScript(clients))
			_, err := View(r.ctx, e.ID, "")
			if err == nil || err.Error() != refusal {
				t.Fatalf("%v", err)
			}
			if len(r.named("focus")) != 0 {
				t.Fatalf("focused after a refusal: %v", r.calls())
			}
		})
	}
	t.Run("explicit", func(t *testing.T) {
		r := newRig(t, baseScript())
		e := r.ready()
		r.panes(viewScript(two))
		if _, err := View(r.ctx, e.ID, "client:x"); err != nil {
			t.Fatal(err)
		}
		focus := r.named("focus")
		if len(focus) != 1 || !slices.Equal(focus[0], []string{"focus", "session:peers", "block:peer", "client:x"}) {
			t.Fatalf("%v", focus)
		}
		if len(r.named("clients")) != 0 {
			t.Fatal("an explicit client needs no listing")
		}
	})
}

func TestViewGonePane(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	script := viewScript(clientsOn("sess"))
	script["probe"] = gone
	r.panes(script)
	_, err := View(r.ctx, e.ID, "")
	if err == nil || !strings.Contains(err.Error(), "pane is gone") {
		t.Fatalf("%v", err)
	}
	if len(r.named("clients")) != 0 || len(r.named("focus")) != 0 {
		t.Fatalf("%v", r.calls())
	}
}

func TestViewRefusesClosedAndUnknownPeer(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	r.panes(viewScript(clientsOn("sess")))
	if _, err := View(r.ctx, "peer-nope", ""); err == nil {
		t.Fatal("want an error for an unknown peer")
	}
	if _, err := r.reg.Update(e.ID, func(x *session.Entry) { x.Closed = true }); err != nil {
		t.Fatal(err)
	}
	if _, err := View(r.ctx, e.ID, ""); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("%v", err)
	}
	if len(r.named("focus")) != 0 {
		t.Fatal("focused a closed peer")
	}
}

func TestPickClient(t *testing.T) {
	app := func(id string, sessions ...string) corepane.Client {
		return corepane.Client{ID: id, Kind: "app", Sessions: sessions}
	}
	cli := corepane.Client{ID: "c-cli", Kind: "cli", Sessions: []string{"s1"}}
	bare := corepane.Client{ID: "c-bare"}
	tests := []struct {
		name     string
		clients  []corepane.Client
		explicit string
		want     string
		fail     bool
	}{
		{"explicit wins", []corepane.Client{app("c1", "s1"), app("c2", "s1")}, "c9", "c9", false},
		{"one match", []corepane.Client{app("c1", "s0", "s1"), app("c2", "s2"), cli, bare}, "", "c1", false},
		{"zero", []corepane.Client{app("c1", "s2"), cli, bare}, "", "", true},
		{"none listed", nil, "", "", true},
		{"two", []corepane.Client{app("c1", "s1"), app("c2", "s1")}, "", "", true},
	}
	for _, tc := range tests {
		got, err := pickClient(tc.clients, tc.explicit, "s1")
		if tc.fail {
			if err == nil || err.Error() != "no single Rex client shows this session; run rex client ls and pass --client <id>" {
				t.Errorf("%s: %q %v", tc.name, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %q %v", tc.name, got, err)
		}
	}
}
