package main

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
)

// leadTrustProject is a project whose bus holds ask 1, person-only, and
// whose registry records the lead lead-<agent> with the native mode native,
// as the lead hook records it from the lead's own payload.
func leadTrustProject(t *testing.T, agent, native string) (stateDir, root string) {
	t.Helper()
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_PEER", "")
	root = t.TempDir()
	stateDir = filepath.Join(root, ".duolog")
	b, err := bus.Open(stateDir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	lead := session.Lead{ID: "lead-" + agent, Target: config.Target{Agent: agent}}
	if _, err := b.Send(bus.Message{From: "duolog/ask.person", To: session.Address(lead), K: gate.Ask, S: "read ../notes.txt", P: true, T: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	// A Codex lead's mode comes from its transcript and carries no native name.
	m := mode.Full
	o := session.LeadObservation{ID: lead.ID, Agent: agent, Mode: &m}
	if agent == "claude" {
		m = mode.Parse(agent, native)
		o.Native = &native
	}
	if _, err := reg.ObserveLead(o, time.Now()); err != nil {
		t.Fatal(err)
	}
	return stateDir, root
}

// modeInput is a lead's hook payload for one Bash call in the native mode native.
func modeInput(id, root, native, command string) []byte {
	data, _ := json.Marshal(map[string]any{"session_id": id, "cwd": root, "tool_name": "Bash", "permission_mode": native, "tool_input": map[string]string{"command": command}})
	return data
}

// personGuarded is one command for each guard that asks the person today.
var personGuarded = map[string]string{
	"person-only allow":     "duolog allow 1",
	"allow without number":  `duolog allow "$N"`,
	"remember outside":      `duolog remember 'cp ~/.ssh/id_rsa k'`,
	"peer show":             "duolog peer show peer-x",
	"telemetry and balance": "duolog telemetry show",
}

// A Claude lead in bypassPermissions or auto decides by itself: no guard asks
// the person, and duolog allow for a person-only ask goes ahead.
func TestSelfDecidingLeadIsNotAsked(t *testing.T) {
	for _, native := range []string{"bypassPermissions", "auto"} {
		stateDir, root := leadTrustProject(t, "claude", native)
		for guard, command := range personGuarded {
			if d, ok := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, native, command)); ok {
				t.Errorf("%s lead, %s (%s): %+v", native, guard, command, d)
			}
		}
	}
}

// A Claude lead in any other mode is asked by every guard exactly as before.
func TestOtherLeadModesAreStillAsked(t *testing.T) {
	for _, native := range []string{"default", "acceptEdits", "plan", "dontAsk", "", "unknown"} {
		stateDir, root := leadTrustProject(t, "claude", native)
		for guard, command := range personGuarded {
			d, ok := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, native, command))
			if !ok || d.Kind != gate.Ask || !strings.Contains(d.Reason, "person") {
				t.Errorf("%q lead, %s (%s): %+v, %v", native, guard, command, d, ok)
			}
		}
	}
}

// The payload and duolog's record of the lead must both say bypass or auto:
// a lead that has just left bypass, or whose record says otherwise, is asked.
func TestPayloadAndRecordMustAgree(t *testing.T) {
	for _, c := range []struct{ record, payload string }{
		{"bypassPermissions", "default"},
		{"auto", "acceptEdits"},
		{"default", "bypassPermissions"},
		{"plan", "auto"},
		{"bypassPermissions", ""},
	} {
		stateDir, root := leadTrustProject(t, "claude", c.record)
		for guard, command := range personGuarded {
			if d, ok := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, c.payload, command)); !ok || d.Kind != gate.Ask {
				t.Errorf("record %q payload %q, %s: %+v, %v", c.record, c.payload, guard, d, ok)
			}
		}
	}
}

// A worker or a peer cannot use the lead's mode: a session that is not a
// registered lead, a worker or peer environment, or a payload that names
// the lead from a project without its record is still asked.
func TestWorkerAndPeerCannotUseLeadMode(t *testing.T) {
	stateDir, root := leadTrustProject(t, "claude", "bypassPermissions")
	for guard, command := range personGuarded {
		// A peer's or worker's own session is not a registered lead.
		if d, ok := guardLeadCall(stateDir, "claude", modeInput("peer-session", root, "bypassPermissions", command)); !ok || d.Kind != gate.Ask {
			t.Errorf("unregistered session, %s: %+v, %v", guard, d, ok)
		}
	}
	for _, env := range []struct{ name, value string }{{"DUOLOG_DEPTH", "1"}, {"DUOLOG_PEER", "peer-x"}} {
		t.Run(env.name, func(t *testing.T) {
			t.Setenv(env.name, env.value)
			for guard, command := range personGuarded {
				if d, ok := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, "bypassPermissions", command)); !ok || d.Kind != gate.Ask {
					t.Errorf("%s=%s, %s: %+v, %v", env.name, env.value, guard, d, ok)
				}
			}
		})
	}
	// No record at all: a state folder without the lead.
	other := t.TempDir()
	if d, ok := guardLeadCall(other, "claude", modeInput("lead-claude", root, "bypassPermissions", "duolog peer show peer-x")); !ok || d.Kind != gate.Ask {
		t.Errorf("no record: %+v, %v", d, ok)
	}
}

// otherLeadAsk registers a second lead, lead-a, in the native mode native,
// and puts its person-only ask on the bus. It returns the ask's number.
func otherLeadAsk(t *testing.T, stateDir, native string) int64 {
	t.Helper()
	reg, err := session.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	m := mode.Parse("claude", native)
	if _, err := reg.ObserveLead(session.LeadObservation{ID: "lead-a", Agent: "claude", Mode: &m, Native: &native}, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(stateDir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	a := session.Lead{ID: "lead-a", Target: config.Target{Agent: "claude"}}
	n, err := b.Send(bus.Message{From: "duolog/ask.other", To: session.Address(a), K: gate.Ask, S: "read ../secret.txt", P: true, W: 120})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A bypass lead decides only its own asks: allowing another lead's
// person-only ask, by number with --lead, with no number, or next to one of
// its own, still asks the person.
func TestSelfDecidingLeadCannotAllowAnotherLeadsAsk(t *testing.T) {
	stateDir, root := leadTrustProject(t, "claude", "bypassPermissions")
	n := otherLeadAsk(t, stateDir, "default")
	other := strconv.FormatInt(n, 10)
	for _, command := range []string{
		"duolog allow " + other + " --lead lead-a",
		"duolog --lead lead-a allow " + other,
		"duolog allow " + other,
		`duolog allow "$N" --lead lead-a`,
		`duolog allow "$N"`,
		"duolog allow 1 && duolog allow " + other + " --lead lead-a",
		"duolog peer show peer-x; duolog allow " + other + " --lead lead-a",
		`duolog remember 'cp ~/.ssh/id_rsa k'; duolog allow ` + other + " --lead lead-a",
		`env X=1 duolog allow ` + other + ` --lead lead-a`,
	} {
		d, ok := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, "bypassPermissions", command))
		if !ok || d.Kind != gate.Ask {
			t.Errorf("%s: %+v, %v", command, d, ok)
		}
	}
	// Its own ask still goes ahead with the other lead's ask pending.
	if d, ok := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, "bypassPermissions", "duolog allow 1")); ok {
		t.Errorf("own ask: %+v", d)
	}
}

// subagentInput is a lead's hook payload for one Bash call that a Claude
// subagent of the lead makes: the lead's session id, the subagent's own id.
func subagentInput(id, root, native, command string) []byte {
	data, _ := json.Marshal(map[string]any{"session_id": id, "agent_id": "child-example", "agent_type": "general-purpose", "cwd": root, "tool_name": "Bash", "permission_mode": native, "tool_input": map[string]string{"command": command}})
	return data
}

// A subagent shares its lead's session id, but its call never uses the
// lead's bypass or auto: every guard asks.
func TestSubagentOfSelfDecidingLeadIsAsked(t *testing.T) {
	for _, native := range []string{"bypassPermissions", "auto"} {
		stateDir, root := leadTrustProject(t, "claude", native)
		for guard, command := range personGuarded {
			if d, ok := guardLeadCall(stateDir, "claude", subagentInput("lead-claude", root, native, command)); !ok || d.Kind != gate.Ask {
				t.Errorf("%s lead, subagent %s: %+v, %v", native, guard, d, ok)
			}
		}
	}
}

// A subagent whose definition sets bypassPermissions, under a lead in
// default, cannot turn the guards off through the lead hook: its payloads do
// not change the lead's recorded mode, so its second call is asked too.
func TestSubagentModeIsNotRecordedForTheLead(t *testing.T) {
	isolatedRouteConfig(t)
	stateDir, root := leadTrustProject(t, "claude", "default")
	t.Chdir(root)
	for call := 1; call <= 2; call++ {
		out, err := gateCommand(t, string(subagentInput("lead-claude", root, "bypassPermissions", "duolog peer show peer-x")), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"permissionDecision":"ask"`) {
			t.Fatalf("call %d not asked: %s", call, out)
		}
	}
	reg, err := session.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	lead, ok, err := reg.Lead("lead-claude")
	if err != nil || !ok || lead.Native != "default" || lead.Mode != mode.Parse("claude", "default") {
		t.Fatalf("lead record %+v, %v, %v; want native default", lead, ok, err)
	}
}

// A Codex lead keeps its deny in full access: its mode comes from its
// transcript file, not from the hook payload.
func TestCodexLeadInFullAccessIsStillDenied(t *testing.T) {
	stateDir, root := leadTrustProject(t, "codex", "danger-full-access")
	for guard, command := range personGuarded {
		if d, ok := guardLeadCall(stateDir, "codex", modeInput("lead-codex", root, "bypassPermissions", command)); !ok || d.Kind != gate.Deny {
			t.Errorf("codex, %s: %+v, %v", guard, d, ok)
		}
	}
}

// Through the installed lead hook: a bypass lead's duolog allow for a
// person-only ask gets no decision from duolog, and a default lead's is asked.
func TestLeadGateLetsSelfDecidingLeadAllow(t *testing.T) {
	for _, c := range []struct {
		native string
		asked  bool
	}{{"bypassPermissions", false}, {"auto", false}, {"default", true}} {
		t.Run(c.native, func(t *testing.T) {
			isolatedRouteConfig(t)
			_, root := leadTrustProject(t, "claude", c.native)
			t.Chdir(root)
			out, err := gateCommand(t, string(modeInput("lead-claude", root, c.native, "duolog allow 1")), "gate", "--agent", "claude", "--event", "PreToolUse")
			if err != nil {
				t.Fatal(err)
			}
			if asked := strings.Contains(out, `"permissionDecision":"ask"`); asked != c.asked {
				t.Fatalf("asked %v, want %v: %s", asked, c.asked, out)
			}
		})
	}
}
