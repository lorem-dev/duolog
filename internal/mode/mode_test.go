package mode

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseModeTable(t *testing.T) {
	for _, tc := range []struct {
		agent, raw string
		want       Mode
	}{
		{"claude", "plan", Plan}, {"claude", "default", Ask}, {"claude", "acceptEdits", Edits}, {"claude", "bypassPermissions", Full},
		{"codex", "read-only", Plan}, {"codex", "on-request", Ask}, {"codex", "workspace-write", Edits}, {"codex", "danger-full-access", Full}, {"codex", "default", Ask},
	} {
		if got := Parse(tc.agent, tc.raw); got != tc.want {
			t.Errorf("Parse(%q,%q)=%q, want %q", tc.agent, tc.raw, got, tc.want)
		}
	}
}
func TestClaudeAutoIsEdits(t *testing.T) {
	if Parse("claude", "auto") != Edits {
		t.Fatal("auto must map to edits")
	}
}
func TestUnknownModeIsAsk(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "unknown"} {
		for _, raw := range []string{"", "unknown", "never"} {
			if Parse(agent, raw) != Ask {
				t.Fatalf("%s %q", agent, raw)
			}
		}
	}
}
func TestModeArgsTable(t *testing.T) {
	for _, tc := range []struct {
		m             Mode
		codex, claude string
	}{{Plan, "read-only", "plan"}, {Ask, "workspace-write", "default"}, {Edits, "workspace-write", "acceptEdits"}, {Full, "workspace-write", "acceptEdits"}} {
		if got := Args("codex", tc.m, "workspace-write"); !reflect.DeepEqual(got, []string{"-s", tc.codex}) {
			t.Errorf("%s: %v", tc.m, got)
		}
		if got := Args("claude", tc.m, "workspace-write"); !reflect.DeepEqual(got, []string{"--permission-mode", tc.claude}) {
			t.Errorf("%s: %v", tc.m, got)
		}
	}
}
func TestNeverDangerousFlags(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, m := range []Mode{Plan, Ask, Edits, Full, "unknown"} {
			for _, sb := range []string{"read-only", "workspace-write", "danger-full-access", ""} {
				got := strings.Join(Args(agent, m, sb), " ")
				if strings.Contains(got, "dangerously") || strings.Contains(got, "bypassPermissions") {
					t.Fatal(got)
				}
			}
		}
	}
}
func TestRoleSandboxNarrowsMode(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, m := range []Mode{Plan, Ask, Edits, Full} {
			if !reflect.DeepEqual(Args(agent, m, "read-only"), Args(agent, Plan, "workspace-write")) {
				t.Fatal(agent, m)
			}
		}
	}
}
func TestLower(t *testing.T) {
	modes := []Mode{Plan, Ask, Edits, Full}
	for i, a := range modes {
		for j, b := range modes {
			want := a
			if j < i {
				want = b
			}
			if Lower(a, b) != want {
				t.Fatal(a, b)
			}
		}
	}
	if Lower(Full, "unknown") != Ask {
		t.Fatal("unknown must narrow to ask")
	}
}
