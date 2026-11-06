package peer

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const (
	recordedApp     = "client:01a00000-0000-7000-8000-000000000001"
	recordedSession = "session:01a00000-0000-7000-8000-0000000000aa"
)

// clientFixture is absolute because the rig changes the working folder.
var clientFixture = func() string {
	abs, err := filepath.Abs("../../rex/testdata/client-ls.json")
	if err != nil {
		panic(err)
	}
	return abs
}()

// clientsOn is the recorded `rex client ls --json` reply with the first
// client's first session renamed to session.
func clientsOn(t *testing.T, session string) string {
	t.Helper()
	data, err := os.ReadFile(clientFixture)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(string(data), recordedSession, session, 1)
}

func viewScript(clients string) map[string]testbin.Scenario {
	script := baseScript()
	script["client ls"] = testbin.Scenario{Stdout: clients}
	script["-C"] = testbin.Scenario{Stdout: "{}"}
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
	r.rex(viewScript(clientsOn(t, "sess")))
	before := sessionsFile(t, r)

	got, err := View(r.ctx, e.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	focus := r.named("-C")
	want := [][]string{
		{"-C", recordedApp, "do", "session.select", "session_id=session:peers"},
		{"-C", recordedApp, "focus", "block:peer", "-s", "session:peers"},
	}
	if !slices.EqualFunc(focus, want, slices.Equal) {
		t.Fatalf("%v", focus)
	}
	if len(r.spawns()) != 0 || len(r.keys()) != 0 || len(r.named("send")) != 0 || len(r.named("block close")) != 0 {
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
	two := strings.Replace(clientsOn(t, "sess"), `"client_id": "client:01a00000-0000-7000-8000-000000000002",`,
		`"client_id": "client:01a00000-0000-7000-8000-000000000002", "session_ids": ["sess"],`, 1)
	if two == clientsOn(t, "sess") {
		t.Fatal("fixture shape changed; the two-client reply was not built")
	}
	for name, clients := range map[string]string{
		"zero": clientsOn(t, "session:elsewhere"),
		"two":  two,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, baseScript())
			e := r.ready()
			r.rex(viewScript(clients))
			_, err := View(r.ctx, e.ID, "")
			if err == nil || err.Error() != refusal {
				t.Fatalf("%v", err)
			}
			if len(r.named("-C")) != 0 || len(r.named("focus")) != 0 {
				t.Fatalf("focused after a refusal: %v", r.calls())
			}
		})
	}
	t.Run("explicit", func(t *testing.T) {
		r := newRig(t, baseScript())
		e := r.ready()
		r.rex(viewScript(two))
		if _, err := View(r.ctx, e.ID, "client:x"); err != nil {
			t.Fatal(err)
		}
		focus := r.named("-C")
		if len(focus) != 2 || !slices.Equal(focus[1], []string{"-C", "client:x", "focus", "block:peer", "-s", "session:peers"}) {
			t.Fatalf("%v", focus)
		}
		if len(r.named("client ls")) != 0 {
			t.Fatal("an explicit client needs no listing")
		}
	})
}

func TestViewGonePane(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	script := viewScript(clientsOn(t, "sess"))
	script["block inspect"] = testbin.Scenario{Exit: 3, Stderr: "Error: block not found"}
	r.rex(script)
	_, err := View(r.ctx, e.ID, "")
	if err == nil || !strings.Contains(err.Error(), "pane is gone") {
		t.Fatalf("%v", err)
	}
	if len(r.named("client ls")) != 0 || len(r.named("-C")) != 0 {
		t.Fatalf("%v", r.calls())
	}
}

func TestViewRefusesClosedAndUnknownPeer(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	r.rex(viewScript(clientsOn(t, "sess")))
	if _, err := View(r.ctx, "peer-nope", ""); err == nil {
		t.Fatal("want an error for an unknown peer")
	}
	if _, err := r.reg.Update(e.ID, func(x *session.Entry) { x.Closed = true }); err != nil {
		t.Fatal(err)
	}
	if _, err := View(r.ctx, e.ID, ""); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("%v", err)
	}
	if len(r.named("-C")) != 0 {
		t.Fatal("focused a closed peer")
	}
}

func TestPickClient(t *testing.T) {
	app := func(id string, sessions ...string) rex.ClientInfo {
		return rex.ClientInfo{ID: id, Kind: "app", Sessions: sessions}
	}
	cli := rex.ClientInfo{ID: "c-cli", Kind: "cli", Sessions: []string{"s1"}}
	bare := rex.ClientInfo{ID: "c-bare"}
	tests := []struct {
		name     string
		clients  []rex.ClientInfo
		explicit string
		want     string
		fail     bool
	}{
		{"explicit wins", []rex.ClientInfo{app("c1", "s1"), app("c2", "s1")}, "c9", "c9", false},
		{"one match", []rex.ClientInfo{app("c1", "s0", "s1"), app("c2", "s2"), cli, bare}, "", "c1", false},
		{"zero", []rex.ClientInfo{app("c1", "s2"), cli, bare}, "", "", true},
		{"none listed", nil, "", "", true},
		{"two", []rex.ClientInfo{app("c1", "s1"), app("c2", "s1")}, "", "", true},
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
