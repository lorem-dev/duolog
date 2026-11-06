package main

import (
	"encoding/json"
	"path/filepath"
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
	data, _ := json.Marshal(map[string]any{"session_id": id,"cwd": root, "tool_name": "Bash", "permission_mode": native, "tool_input": map[string]string{"command": command}})
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
