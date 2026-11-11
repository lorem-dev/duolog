package gate

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// A worker in a submodule is bounded by the settings of its lead's own root,
// the superproject, and by those of its work root; the stricter wins.
func TestSubmoduleLeadDenyCeiling(t *testing.T) {
	p := testPolicy(t)
	f := submodule.New(t)
	t.Run("claude", func(t *testing.T) {
		testClaudeLeadCeiling(t, p, f)
	})
	t.Run("codex", func(t *testing.T) {
		testCodexLeadCeiling(t, p, f)
	})
	t.Run("malformed settings at either root", func(t *testing.T) {
		testMalformedLeadCeiling(t, f)
	})
	t.Run("legacy lead without a recorded root", func(t *testing.T) {
		reg := &session.Registry{Dir: filepath.Join(f.Superproject, ".duolog")}
		if got := LeadRoot(session.Lead{}, reg); got != f.Superproject {
			t.Fatalf("legacy lead root = %q, want %q", got, f.Superproject)
		}
		if got := LeadRoot(session.Lead{Root: f.SuperLinked}, reg); got != f.SuperLinked {
			t.Fatalf("recorded lead root = %q, want %q", got, f.SuperLinked)
		}
	})
}

func testClaudeLeadCeiling(t *testing.T, p config.Policy, f submodule.Fixture) {
	t.Helper()
	writeSetting(t, filepath.Join(f.Superproject, ".claude/settings.json"),
		`{"permissions":{"deny":["Bash(git push:*)","Read(secrets/**)","Edit(module/locked/**)"]}}`)
	writeSetting(t, filepath.Join(f.Submodule, ".claude/settings.json"),
		`{"permissions":{"deny":["Bash(rm:*)","Edit(own/**)"]}}`)
	writeSetting(t, filepath.Join(f.SuperLinked, ".claude/settings.local.json"),
		`{"permissions":{"deny":["Bash(curl:*)"]}}`)
	for _, tc := range []struct {
		name, work, lead string
		deny, allow      []Action
	}{
		{
			name: "superproject lead, submodule worker", work: f.Submodule, lead: f.Superproject,
			deny: []Action{
				{Kind: "exec", Target: "git push origin main"}, {Kind: "exec", Target: "rm -rf build"},
				{Kind: "write", Target: "locked/a.go"}, {Kind: "read", Target: "../secrets/key"},
				{Kind: "write", Target: "own/a.go"},
			},
			allow: []Action{{Kind: "write", Target: "src/a.go"}},
		},
		{
			name: "superproject lead, linked submodule worker", work: f.Linked, lead: f.Superproject,
			deny: []Action{
				{Kind: "exec", Target: "git push origin main"}, {Kind: "exec", Target: "rm -rf build"},
				{Kind: "write", Target: "own/a.go"},
			},
			allow: []Action{{Kind: "write", Target: "src/a.go"}},
		},
		{
			name: "lead in a linked superproject checkout", work: f.SuperLinkedSubmodule, lead: f.SuperLinked,
			deny: []Action{
				{Kind: "exec", Target: "git push origin main"}, {Kind: "exec", Target: "curl example.com"},
				{Kind: "write", Target: "locked/a.go"},
			},
			allow: []Action{{Kind: "write", Target: "src/a.go"}},
		},
		{
			name: "lead started in the submodule, superproject worker", work: f.Superproject, lead: f.Submodule,
			deny: []Action{
				{Kind: "exec", Target: "rm -rf build"}, {Kind: "exec", Target: "git push origin main"},
				{Kind: "write", Target: "module/own/a.go"},
			},
			allow: []Action{{Kind: "write", Target: "own/a.go"}, {Kind: "write", Target: "src/a.go"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			combined, err := ImportLeadForWork(tc.work, tc.lead, "claude")
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range tc.deny {
				a.Role = "implement"
				check(t, p, combined, mode.Full, a, Deny)
			}
			for _, a := range tc.allow {
				a.Role = "implement"
				check(t, p, combined, mode.Full, a, Allow)
			}
			assertUniqueDenies(t, combined.Deny)
		})
	}
	combined, err := ImportLeadForWork(f.Submodule, f.Superproject, "claude")
	if err != nil {
		t.Fatal(err)
	}
	// The lead's relative selectors keep their match and carry the submodule's
	// path as their anchor; the lead's files outside it keep the ../ form.
	for _, want := range []config.Rule{
		{Action: "read", Match: "secrets/**", Under: "module"},
		{Action: "read", Match: "../secrets/**"},
		{Action: "write", Match: "module/locked/**", Under: "module"},
	} {
		want.Decision = Deny
		if !slices.ContainsFunc(combined.Deny, func(have config.Rule) bool { return sameRule(have, want) }) {
			t.Fatalf("no %+v in %+v", want, combined.Deny)
		}
	}
	if got := denyMatch(combined.Deny, "write", "locked"); got != "module/locked/**" {
		t.Fatal(got)
	}
}

// A lead's glob reaches into the submodule as it reaches any folder of the
// superproject: "**" spans the submodule's path, a literal prefix names it,
// and a linked worktree of the submodule is the same project.
func TestSubmoduleLeadDenyGlobsReachSubmodule(t *testing.T) {
	p := testPolicy(t)
	f := submodule.New(t)
	writeSetting(t, filepath.Join(f.Superproject, ".claude/settings.json"),
		`{"permissions":{"deny":["Read(**/private/**)","Edit(module/locked/**)","Edit(*/frozen/**)"]}}`)
	for _, work := range []string{f.Submodule, f.Linked} {
		combined, err := ImportLeadForWork(work, f.Superproject, "claude")
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range []Action{
			{Kind: "read", Target: "private/data.txt"}, {Kind: "read", Target: "src/private/key"},
			{Kind: "write", Target: "locked/a.go"}, {Kind: "write", Target: "frozen/a.go"},
		} {
			a.Role = "implement"
			check(t, p, combined, mode.Full, a, Deny)
		}
		a := Action{Kind: "write", Target: "src/a.go", Role: "implement"}
		check(t, p, combined, mode.Full, a, Allow)
	}
}

// A lead's deny names, for a submodule worker, exactly what the lead's own
// settings deny there: globs, braces and case are read where the lead wrote
// them, whichever checkout of the superproject the lead or the worker is in.
func TestSubmoduleLeadDenyAnchoredInEveryCheckout(t *testing.T) {
	p := testPolicy(t)
	f := submodule.New(t)
	writeSetting(t, filepath.Join(f.Superproject, ".claude/settings.json"), `{"permissions":{"deny":[
		"Read(**/private/**)","Read(**/.env)","Edit(**/*.key)",
		"Edit({module/locked,other}/**)","Edit(MODULE/frozen/**)","Edit(module/nested/sealed/**)"]}}`)
	deny := []Action{
		{Kind: "read", Target: "private/data.txt"}, {Kind: "read", Target: "src/private/key"},
		{Kind: "read", Target: ".env"}, {Kind: "read", Target: "config/.env"},
		{Kind: "write", Target: "a.key"}, {Kind: "write", Target: "certs/server.key"},
		{Kind: "write", Target: "locked/a.go"}, {Kind: "write", Target: "frozen/a.go"},
		{Kind: "write", Target: "FROZEN/a.go"}, {Kind: "write", Target: "nested/sealed/a.go"},
		{Kind: "exec", Target: "cat config/.env"}, {Kind: "exec", Target: "echo x > locked/a.go"},
	}
	allow := []Action{
		{Kind: "write", Target: "src/a.go"}, {Kind: "write", Target: "other/a.go"},
		{Kind: "read", Target: "public/data.txt"}, {Kind: "read", Target: "env.txt"},
		{Kind: "write", Target: "a.keys"}, {Kind: "write", Target: "sealed/a.go"},
	}
	for _, tc := range []struct{ name, work, lead string }{
		{"main lead, submodule worker", f.Submodule, f.Superproject},
		{"main lead, linked submodule worker", f.Linked, f.Superproject},
		{"main lead, worker in a sibling checkout's submodule", f.SuperLinkedSubmodule, f.Superproject},
		{"linked lead, submodule worker", f.Submodule, f.SuperLinked},
		{"linked lead, linked submodule worker", f.Linked, f.SuperLinked},
		{"linked lead, worker in its own submodule", f.SuperLinkedSubmodule, f.SuperLinked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			combined, err := ImportLeadForWork(tc.work, tc.lead, "claude")
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range deny {
				a.Role = "implement"
				check(t, p, combined, mode.Full, a, Deny)
			}
			for _, a := range allow {
				a.Role = "implement"
				check(t, p, combined, mode.Full, a, Allow)
			}
		})
	}
	t.Run("nested submodule worker", func(t *testing.T) {
		combined, err := ImportLeadForWork(f.Nested, f.Superproject, "claude")
		if err != nil {
			t.Fatal(err)
		}
		check(t, p, combined, mode.Full, Action{Kind: "write", Target: "sealed/a.go", Role: "implement"}, Deny)
		check(t, p, combined, mode.Full, Action{Kind: "write", Target: "src/a.go", Role: "implement"}, Allow)
	})
}

// A Codex lead's settings carry no path rule, only a mode, and that mode
// bounds a submodule worker in every checkout of the superproject.
func TestSubmoduleCodexLeadInEveryCheckout(t *testing.T) {
	p := testPolicy(t)
	f := submodule.New(t)
	writeSetting(t, filepath.Join(f.Superproject, ".codex/config.toml"), "sandbox_mode = 'read-only'\n")
	for _, tc := range []struct{ work, lead string }{
		{f.Submodule, f.Superproject}, {f.Linked, f.Superproject}, {f.SuperLinkedSubmodule, f.Superproject},
		{f.Submodule, f.SuperLinked}, {f.Linked, f.SuperLinked}, {f.SuperLinkedSubmodule, f.SuperLinked},
	} {
		combined, err := ImportLeadForWork(tc.work, tc.lead, "codex")
		if err != nil {
			t.Fatal(err)
		}
		if combined.Mode != mode.Plan || len(combined.Deny) != 0 {
			t.Fatalf("%s under %s: %+v", tc.work, tc.lead, combined)
		}
		if d := Evaluate(p, combined, mode.Full, Action{Kind: "write", Target: "src/a.go", Role: "implement"}); d.Kind != Deny || d.Mode != mode.Plan {
			t.Fatalf("%s under %s: write %+v", tc.work, tc.lead, d)
		}
		if d := Evaluate(p, combined, mode.Full, Action{Kind: "read", Target: "src/a.go", Role: "implement"}); d.Kind != Allow {
			t.Fatalf("%s under %s: read %+v", tc.work, tc.lead, d)
		}
	}
}

// A lead on one drive whose absolute selector names a linked submodule
// worktree on another drive denies the files it names there and no others.
func TestSubmoduleLeadAbsoluteSelectorOnAnotherDrive(t *testing.T) {
	p := testPolicy(t)
	lead, work := `C:/trees/super`, `D:/trees/module`
	rule, err := claudeDeny("Edit(//D:/trees/module/locked/**)")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Match, err = claudePathPattern(rule.Match, lead, `C:/Users/u`, lead); err != nil {
		t.Fatal(err)
	}
	// Found by Git or not, an absolute selector compares the absolute target.
	for _, place := range []leadPlace{{anchor: "module", found: true}, {}} {
		rules := LeadRules{Deny: leadDenyForWork(rule, lead, work, place)}
		for _, target := range []string{"locked/a.go", "LOCKED/a.go", "D:/trees/module/locked/a.go"} {
			check(t, p, rules, mode.Full, Action{Kind: "write", Target: target, Role: "implement"}, Deny)
		}
		check(t, p, rules, mode.Full, Action{Kind: "write", Target: "src/a.go", Role: "implement"}, Allow)
	}
}

// A Git failure while placing the work against the lead fails the import, so
// the gate denies, instead of falling back to moved forms that miss globs
// and case.
func TestSubmoduleLeadDenyFailsClosedOnGitFailure(t *testing.T) {
	f := submodule.New(t)
	writeSetting(t, filepath.Join(f.Superproject, ".claude/settings.json"),
		`{"permissions":{"deny":["Read(*/vault/**)"]}}`)
	t.Setenv("PATH", t.TempDir())
	_, err := ImportLeadForWork(f.Submodule, f.Superproject, "claude")
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatal("a Git failure did not fail the import", err)
	}
}

// A work root inside the lead's root is bounded by the lead's rules read where
// the lead wrote them, Git relation or not.
func TestLeadDenyAnchoredAtWorkInsideLeadRoot(t *testing.T) {
	p := testPolicy(t)
	f := submodule.New(t)
	writeSetting(t, filepath.Join(f.Superproject, ".claude/settings.json"),
		`{"permissions":{"deny":["Read(*/vault/**)","Read(UNRELATED/private/**)"]}}`)
	combined, err := ImportLeadForWork(f.Unrelated, f.Superproject, "claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"vault/key", "private/key"} {
		check(t, p, combined, mode.Full, Action{Kind: "read", Target: target, Role: "implement"}, Deny)
	}
	check(t, p, combined, mode.Full, Action{Kind: "read", Target: "src/a.go", Role: "implement"}, Allow)
	assertUniqueDenies(t, combined.Deny)
}

// A worker whose checkout holds the lead's linked checkout keeps every moved
// form of the lead's selectors: they name the lead's files inside the work.
func TestLeadDenyKeepsMovedFormsWhenLeadInsideWork(t *testing.T) {
	p := testPolicy(t)
	main := filepath.Join(t.TempDir(), "super")
	submodule.Init(t, main)
	lead := filepath.Join(main, ".worktrees", "feat")
	submodule.Git(t, main, "worktree", "add", "-q", "--detach", lead)
	writeSetting(t, filepath.Join(lead, ".claude/settings.local.json"),
		`{"permissions":{"deny":["Read(vault/**)"]}}`)
	combined, err := ImportLeadForWork(main, lead, "claude")
	if err != nil {
		t.Fatal(err)
	}
	check(t, p, combined, mode.Full, Action{Kind: "read", Target: ".worktrees/feat/vault/x", Role: "implement"}, Deny)
	check(t, p, combined, mode.Full, Action{Kind: "read", Target: "src/a.go", Role: "implement"}, Allow)
}

func testCodexLeadCeiling(t *testing.T, p config.Policy, f submodule.Fixture) {
	t.Helper()
	for _, tc := range []struct{ name, super, sub string }{
		{"lower mode at the lead root", "sandbox_mode = 'read-only'\n", "approval_policy = 'on-request'\n"},
		{"lower mode at the work root", "approval_policy = 'untrusted'\n", "sandbox_mode = 'read-only'\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeSetting(t, filepath.Join(f.Superproject, ".codex/config.toml"), tc.super)
			writeSetting(t, filepath.Join(f.Submodule, ".codex/config.toml"), tc.sub)
			combined, err := ImportLeadForWork(f.Submodule, f.Superproject, "codex")
			if err != nil {
				t.Fatal(err)
			}
			if combined.Mode != mode.Plan {
				t.Fatal(combined.Mode)
			}
			d := Evaluate(p, combined, mode.Full, Action{Kind: "write", Target: "src/a.go", Role: "implement"})
			if d.Kind != Deny {
				t.Fatal(d)
			}
		})
	}
}

func testMalformedLeadCeiling(t *testing.T, f submodule.Fixture) {
	t.Helper()
	for _, tc := range []struct{ name, agent, broken, data, valid string }{
		{"claude lead root", "claude", filepath.Join(f.Superproject, ".claude/settings.json"), `{"permissions":{"deny":"x"}}`, `{}`},
		{"claude work root", "claude", filepath.Join(f.Submodule, ".claude/settings.json"), `broken`, `{}`},
		{"codex lead root", "codex", filepath.Join(f.Superproject, ".codex/config.toml"), "sandbox_mode = [\n", ""},
		{"codex work root", "codex", filepath.Join(f.Submodule, ".codex/config.toml"), "approval_policy = 'sometimes'\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeSetting(t, tc.broken, tc.valid)
			if _, err := ImportLeadForWork(f.Submodule, f.Superproject, tc.agent); err != nil {
				t.Fatalf("valid settings refused: %v", err)
			}
			writeSetting(t, tc.broken, tc.data)
			_, malformedErr := ImportLeadForWork(f.Submodule, f.Superproject, tc.agent)
			if malformedErr == nil {
				t.Fatal("unreadable ceiling did not fail closed")
			}
			writeSetting(t, tc.broken, tc.valid)
		})
	}
}

func denyMatch(rules []config.Rule, action, part string) string {
	for _, rule := range rules {
		if rule.Action == action && strings.Contains(rule.Match, part) {
			return rule.Match
		}
	}
	return ""
}

func assertUniqueDenies(t *testing.T, rules []config.Rule) {
	t.Helper()
	seen := map[string]bool{}
	for _, rule := range rules {
		key := rule.Action + "\x00" + rule.Match + "\x00" + rule.Under + "\x00" + rule.Decision
		if seen[key] {
			t.Fatalf("duplicate deny %+v", rule)
		}
		seen[key] = true
	}
}
