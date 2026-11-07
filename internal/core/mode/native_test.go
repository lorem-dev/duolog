package mode

import (
	"reflect"
	"testing"
)

func TestResolveTable(t *testing.T) {
	for _, tc := range []struct {
		agent, req          string
		lead, eff           Mode
		leadNative, sandbox string
		native              string
		level               Mode
		by                  string
		looser              bool
	}{
		{"claude", "auto", Edits, Edits, "acceptEdits", "workspace-write", "acceptEdits", Edits, "lead edits", false},
		{"claude", "auto", Edits, Edits, "auto", "workspace-write", "auto", Edits, "", false},
		{"claude", "auto", Full, Full, "bypassPermissions", "workspace-write", "auto", Edits, "", true},
		{"claude", "bypassPermissions", Edits, Edits, "acceptEdits", "workspace-write", "acceptEdits", Edits, "lead edits", false},
		{"claude", "bypassPermissions", Full, Full, "bypassPermissions", "workspace-write", "bypassPermissions", Full, "", true},
		{"claude", "bypassPermissions", Full, Edits, "bypassPermissions", "workspace-write", "acceptEdits", Edits, "duolog mode", false},
		{"claude", "bypassPermissions", Full, Full, "bypassPermissions", "read-only", "default", Plan, "role sandbox", false},
		// Claude's plan waits for a person; the gate keeps level plan read-only.
		{"claude", "plan", Full, Full, "bypassPermissions", "workspace-write", "default", Plan, "", false},
		{"claude", "plan", Plan, Plan, "plan", "read-only", "default", Plan, "", false},
		{"claude", "default", Full, Full, "bypassPermissions", "read-only", "default", Plan, "role sandbox", false},
		{"codex", "read-only", Full, Full, "", "workspace-write", "read-only", Plan, "", false},
		{"codex", "workspace-write", Ask, Ask, "", "workspace-write", "workspace-write", Ask, "lead ask", false},
		{"codex", "danger-full-access", Full, Full, "", "workspace-write", "workspace-write", Full, "not offered", false},
		// Parse reads an unknown name as ask, so the level drops too.
		{"claude", "dontAsk", Full, Full, "bypassPermissions", "workspace-write", "default", Ask, "not offered", false},
	} {
		c := NewCeiling("claude", tc.leadNative, tc.lead, tc.eff)
		got := Resolve(tc.agent, Request{Native: tc.req, Source: "role implement"}, c, tc.sandbox)
		if got.Native != tc.native || got.Level != tc.level || got.LoweredBy != tc.by || got.Looser != tc.looser || got.Source != "role implement" {
			t.Errorf("%s %s lead %s/%s %s: %+v", tc.agent, tc.req, tc.lead, tc.eff, tc.leadNative, got)
		}
	}
}

func TestResolveWithoutEntryIsArgs(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, m := range []Mode{Plan, Ask, Edits, Full} {
			for _, sb := range []string{"read-only", "workspace-write"} {
				c := NewCeiling("claude", "bypassPermissions", m, m)
				got := Resolve(agent, Request{}, c, sb)
				if !reflect.DeepEqual(Flags(agent, got.Native), ArgsFor(agent, m, sb, c.Auto)) || got.Source != "default" || got.Looser {
					t.Errorf("%s %s %s: %+v", agent, m, sb, got)
				}
			}
		}
	}
}

// Every request, ceiling and sandbox: the level is at most the effective
// lead mode, the flag at most the level (or duolog's own default flag for
// that level), never a flag duolog does not offer.
func TestResolveNeverAboveLead(t *testing.T) {
	names := []string{"", "plan", "default", "acceptEdits", "auto", "bypassPermissions", "dontAsk", "read-only", "workspace-write", "danger-full-access", "Auto"}
	modes := []Mode{Plan, Ask, Edits, Full}
	for _, agent := range []string{"claude", "codex"} {
		for _, req := range names {
			for _, lead := range modes {
				for _, eff := range modes {
					if Lower(eff, lead) != eff {
						continue
					}
					for _, native := range []string{"", "acceptEdits", "auto", "bypassPermissions"} {
						for _, sb := range []string{"read-only", "workspace-write"} {
							c := NewCeiling("claude", native, lead, eff)
							got := Resolve(agent, Request{Native: req}, c, sb)
							_, ok := Rank(agent, got.Native)
							parsed := Parse(agent, got.Native)
							// Codex has no flag between read-only and
							// workspace-write, so its default at ask is the
							// latter.
							isDefault := got.Native == Default(agent, got.Level, sb) || got.Native == DefaultFor(agent, got.Level, sb, c.Auto)
							if !ok || Lower(got.Level, eff) != got.Level || (!isDefault && Lower(parsed, got.Level) != parsed) {
								t.Fatalf("%s %q lead %s eff %s: %+v", agent, req, lead, eff, got)
							}
							if sb == "read-only" && got.Level != Plan {
								t.Fatalf("read-only sandbox gave %+v", got)
							}
							if agent == "claude" && got.Native == "plan" {
								t.Fatalf("claude worker in plan: %q lead %s eff %s: %+v", req, lead, eff, got)
							}
							if agent == "claude" && got.Native == "auto" && !c.Auto {
								t.Fatalf("auto without an auto lead: %+v %+v", c, got)
							}
						}
					}
				}
			}
		}
	}
}

func TestLowerNative(t *testing.T) {
	for _, tc := range []struct{ agent, a, b, want string }{
		{"claude", "auto", "acceptEdits", "acceptEdits"}, {"claude", "", "plan", "plan"},
		{"claude", "bypassPermissions", "", "bypassPermissions"}, {"codex", "workspace-write", "read-only", "read-only"},
		{"claude", "auto", "dontAsk", "dontAsk"}, {"claude", "", "", ""},
	} {
		if got := LowerNative(tc.agent, tc.a, tc.b); got != tc.want {
			t.Errorf("LowerNative(%q,%q,%q)=%q want %q", tc.agent, tc.a, tc.b, got, tc.want)
		}
	}
}

// A Claude worker or peer starts in auto when the lead's ceiling allows it;
// every other case keeps its earlier default.
func TestClaudeStartsInAutoWhenCeilingAllows(t *testing.T) {
	for _, tc := range []struct {
		name, agent, leadNative, req, sandbox string
		lead, eff                             Mode
		want                                  string
	}{
		{"full lead", "claude", "bypassPermissions", "", "workspace-write", Full, Full, "auto"},
		{"codex lead at full", "claude", "", "", "workspace-write", Full, Full, "auto"},
		{"claude auto lead at edits", "claude", "auto", "", "workspace-write", Edits, Edits, "auto"},
		{"edits lead", "claude", "acceptEdits", "", "workspace-write", Edits, Edits, "acceptEdits"},
		{"full lead lowered to edits by duolog mode", "claude", "bypassPermissions", "", "workspace-write", Full, Edits, "acceptEdits"},
		{"read-only role", "claude", "bypassPermissions", "", "read-only", Full, Full, "default"},
		{"plan", "claude", "bypassPermissions", "", "workspace-write", Full, Plan, "default"},
		{"entry lowers to acceptEdits", "claude", "bypassPermissions", "acceptEdits", "workspace-write", Full, Full, "acceptEdits"},
		{"entry asks bypassPermissions", "claude", "bypassPermissions", "bypassPermissions", "workspace-write", Full, Full, "bypassPermissions"},
		{"codex unchanged", "codex", "bypassPermissions", "", "workspace-write", Full, Full, "workspace-write"},
	} {
		c := NewCeiling("claude", tc.leadNative, tc.lead, tc.eff)
		got := Resolve(tc.agent, Request{Native: tc.req, Source: "x"}, c, tc.sandbox)
		if got.Native != tc.want {
			t.Errorf("%s: native %q, want %q (%+v)", tc.name, got.Native, tc.want, got)
		}
		if tc.req == "" && got.Looser {
			t.Errorf("%s: the default is never looser than itself: %+v", tc.name, got)
		}
	}
	if got := ArgsFor("claude", Full, "workspace-write", true); !reflect.DeepEqual(got, []string{"--permission-mode", "auto"}) {
		t.Errorf("ArgsFor full auto = %v", got)
	}
	if got := ArgsFor("claude", Full, "workspace-write", false); !reflect.DeepEqual(got, []string{"--permission-mode", "acceptEdits"}) {
		t.Errorf("ArgsFor full = %v", got)
	}
	if got := ArgsFor("claude", Plan, "workspace-write", true); !reflect.DeepEqual(got, []string{"--permission-mode", "default"}) {
		t.Errorf("ArgsFor plan auto = %v", got)
	}
}

func TestRankAndFlags(t *testing.T) {
	for name, want := range map[string]int{"plan": 0, "default": 1, "acceptEdits": 2, "auto": 3, "bypassPermissions": 4} {
		if got, ok := Rank("claude", name); !ok || got != want {
			t.Errorf("Rank(claude,%q)=%d,%v", name, got, ok)
		}
	}
	for _, name := range []string{"dontAsk", "Auto", "read-only", ""} {
		if got, ok := Rank("claude", name); ok || got != -1 {
			t.Errorf("Rank(claude,%q)=%d,%v", name, got, ok)
		}
	}
	if got, ok := Rank("codex", "danger-full-access"); ok || got != -1 {
		t.Errorf("danger-full-access ranked: %d,%v", got, ok)
	}
	if !reflect.DeepEqual(Flags("claude", "auto"), []string{"--permission-mode", "auto"}) ||
		!reflect.DeepEqual(Flags("codex", "read-only"), []string{"-s", "read-only"}) || Flags("other", "x") != nil {
		t.Error("Flags")
	}
}
