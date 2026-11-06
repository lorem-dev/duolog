package peer

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

const (
	firstSession = "session:00000000-0001-7000-8000-000000000001"
	newSession   = "session:00000000-0002-7000-8000-000000000002"
	lastSession  = "session:00000000-0003-7000-8000-000000000003"
)

func projectSessionLabel(r *rig) string { return "duolog | " + filepath.Base(r.root) }

// owns records sessions as created for the rig's project, as an earlier open
// of this project would have.
func (r *rig) owns(ids ...string) {
	r.t.Helper()
	for _, id := range ids {
		if err := r.reg.SetPeerSessionRoot(id, canonicalRoot(r.root)); err != nil {
			r.t.Fatal(err)
		}
	}
}

func sessionNameScript(t *testing.T, sessions ...[2]string) map[string]testbin.Scenario {
	t.Helper()
	items := make([]map[string]string, 0, len(sessions))
	for _, item := range sessions {
		items = append(items, map[string]string{"session_id": item[0], "label": item[1]})
	}
	data, err := json.Marshal(map[string]any{"sessions": items})
	if err != nil {
		t.Fatal(err)
	}
	script := baseScript()
	script["new"] = testbin.Scenario{Stdout: `{"session_id":"` + newSession + `","block_ids":["block:peer"]}`}
	script["session list"] = testbin.Scenario{Stdout: string(data)}
	script["session rename"] = testbin.Scenario{}
	return script
}

func assertSessionRenames(t *testing.T, r *rig, sessions, suffixes []string) {
	t.Helper()
	got := r.named("session rename")
	if len(got) != len(sessions) {
		t.Fatalf("renames: %v, want sessions %v", got, sessions)
	}
	for i, id := range sessions {
		want := []string{"session", "rename", id, projectSessionLabel(r) + suffixes[i]}
		if !slices.Equal(got[i], want) {
			t.Fatalf("rename %d: %v, want %v", i, got[i], want)
		}
	}
	if len(r.named("window rename")) != 0 {
		t.Fatal("renamed peer tabs")
	}
}

func TestProjectSessionNameSingle(t *testing.T) {
	r := newRig(t, baseScript())
	label := projectSessionLabel(r)
	r.rex(sessionNameScript(t, [2]string{newSession, label}))
	r.ready()
	assertSessionRenames(t, r, nil, nil)
	args := r.spawns()[0]
	i := slices.Index(args, "--window")
	if args[1] != label || i < 0 || args[i+1] != "duolog | codex-implement" {
		t.Fatalf("session or tab label changed: %v", args)
	}
}

func TestProjectSessionNamesExcludeLeadSession(t *testing.T) {
	for _, suffix := range []string{"", " (1)"} {
		t.Run("lead"+suffix, func(t *testing.T) {
			r := newRig(t, baseScript())
			label := projectSessionLabel(r)
			r.rex(sessionNameScript(t,
				[2]string{"sess", label + suffix},
				[2]string{newSession, label + " (2)"},
			))
			r.ready()
			assertSessionRenames(t, r, []string{newSession}, []string{""})
		})
	}
}

func TestProjectSessionNamesSkipUnchangedLabels(t *testing.T) {
	r := newRig(t, baseScript())
	label := projectSessionLabel(r)
	r.owns(firstSession)
	r.rex(sessionNameScript(t,
		[2]string{firstSession, label + " (1)"},
		[2]string{newSession, label},
	))
	r.ready()
	assertSessionRenames(t, r, []string{newSession}, []string{" (2)"})
}

func TestProjectSessionNamesSecondCreated(t *testing.T) {
	r := newRig(t, baseScript())
	label := projectSessionLabel(r)
	r.owns(firstSession)
	// Listing order is reversed; UUIDv7 session IDs determine creation order.
	r.rex(sessionNameScript(t, [2]string{newSession, label}, [2]string{firstSession, label}))
	r.ready()
	assertSessionRenames(t, r, []string{firstSession, newSession}, []string{" (1)", " (2)"})
}

func TestProjectSessionNamesAfterKill(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			r := newRig(t, baseScript())
			label := projectSessionLabel(r)
			r.owns(firstSession, lastSession)
			r.rex(sessionNameScript(t, [2]string{newSession, label}))
			peer := r.ready()
			remaining := [][2]string{{firstSession, label + " (1)"}}
			ids, suffixes := []string{firstSession}, []string{""}
			if count == 2 {
				remaining = append([][2]string{{lastSession, label + " (3)"}}, remaining...)
				ids, suffixes = []string{lastSession}, []string{" (2)"}
			}
			r.rex(sessionNameScript(t, remaining...))
			if err := Close(r.ctx, peer.ID); err != nil {
				t.Fatal(err)
			}
			assertSessionRenames(t, r, ids, suffixes)
			if got := r.named("kill"); len(got) != 1 || !slices.Equal(got[0], []string{"kill", newSession}) {
				t.Fatal(got)
			}
			calls := r.calls()
			kill, listing := -1, -1
			for i, args := range calls {
				if args[0] == "kill" {
					kill = i
				}
				if slices.Equal(args, []string{"session", "list", "--json"}) {
					listing = i
				}
			}
			if kill < 0 || listing <= kill {
				t.Fatalf("must list surviving sessions after kill: %v", calls)
			}
		})
	}
}

// Two projects whose folders share a base name keep their own numbering: the
// other project's session has the same visible label but another root.
func TestProjectSessionNamesGroupByRootNotBaseName(t *testing.T) {
	r := newRig(t, baseScript())
	label := projectSessionLabel(r)
	other := filepath.Join(t.TempDir(), filepath.Base(r.root))
	if err := r.reg.SetPeerSessionRoot(firstSession, canonicalRoot(other)); err != nil {
		t.Fatal(err)
	}
	r.rex(sessionNameScript(t, [2]string{firstSession, label}, [2]string{newSession, label}))
	r.ready()
	// This project has one session, so it stays unnumbered; the other
	// project's session is left alone.
	assertSessionRenames(t, r, nil, nil)
	roots, err := r.reg.PeerSessionRoots()
	if err != nil {
		t.Fatal(err)
	}
	if roots[newSession] != canonicalRoot(r.root) || roots[firstSession] != canonicalRoot(other) {
		t.Fatalf("recorded roots: %v", roots)
	}
	if args := r.spawns()[0]; args[1] != label {
		t.Fatalf("visible name is not the base name: %v", args)
	}
}

func TestProjectSessionNamesIgnoreOtherSessions(t *testing.T) {
	r := newRig(t, baseScript())
	label := projectSessionLabel(r)
	r.rex(sessionNameScript(t,
		[2]string{newSession, label + " (7)"},
		[2]string{"session:other", "duolog | other-project"},
		[2]string{"session:lead", "shell"},
		[2]string{"session:prefix", label + "-other"},
		[2]string{"session:suffix", label + " (1) extra"},
		[2]string{"session:word", label + " (plan)"},
		[2]string{"session:empty", label + " ()"},
	))
	r.ready()
	assertSessionRenames(t, r, []string{newSession}, []string{""})
}

func TestProjectSessionNameFailureDoesNotFailOpenOrClose(t *testing.T) {
	for _, operation := range []string{"open", "close"} {
		t.Run(operation, func(t *testing.T) {
			r := newRig(t, baseScript())
			label := projectSessionLabel(r)
			r.owns(firstSession)
			script := sessionNameScript(t, [2]string{newSession, label}, [2]string{firstSession, label})
			script["session rename"] = testbin.Scenario{Exit: 4}
			r.rex(script)
			peer := r.ready()
			if operation == "open" {
				assertSessionRenames(t, r, []string{firstSession, newSession}, []string{" (1)", " (2)"})
				return
			}
			script = sessionNameScript(t, [2]string{firstSession, label + " (1)"})
			script["session rename"] = testbin.Scenario{Exit: 4}
			r.rex(script)
			if err := Close(r.ctx, peer.ID); err != nil {
				t.Fatal(err)
			}
			assertSessionRenames(t, r, []string{firstSession}, []string{""})
			if id, err := r.reg.PeerRexSession(); err != nil || id != "" {
				t.Fatalf("cleanup identity not cleared: %q %v", id, err)
			}
		})
	}
}

func TestProjectSessionNameListingFailureDoesNotFailOpenOrClose(t *testing.T) {
	for _, scenario := range []testbin.Scenario{{Exit: 4}, {Stdout: `not JSON`}} {
		r := newRig(t, baseScript())
		script := sessionNameScript(t)
		script["session list"] = scenario
		r.rex(script)
		peer := r.ready()
		if len(r.named("session list")) != 1 || len(r.named("session rename")) != 0 {
			t.Fatal(r.calls())
		}
		r.rex(script)
		if err := Close(r.ctx, peer.ID); err != nil {
			t.Fatal(err)
		}
		if len(r.named("session list")) != 1 || len(r.named("session rename")) != 0 {
			t.Fatal(r.calls())
		}
	}
}

func TestProjectSessionNamesDoNotChangeWhenTabsOpenOrClose(t *testing.T) {
	r := newRig(t, baseScript())
	first := r.ready()
	script := withScreens(claudeIdle(""))
	script["window new"] = testbin.Scenario{Stdout: `{"block_ids":["block:second"]}`}
	r.rex(script)
	r.open("plan")
	args := r.named("window new")[0]
	if !slices.Contains(args, "duolog | claude-plan") {
		t.Fatalf("peer tab label changed: %v", args)
	}
	if err := Close(r.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.named("session list")) != 0 || len(r.named("session rename")) != 0 || len(r.named("window rename")) != 0 {
		t.Fatal("tab changes affected session names", r.calls())
	}
}
