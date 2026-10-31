package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

func policy(t testing.TB) config.Policy {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	c, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c.Policy
}

func check(t *testing.T, p config.Policy, lead LeadRules, m mode.Mode, a Action, want string) {
	t.Helper()
	d := Evaluate(p, lead, m, a)
	if d.Kind != want || d.Reason == "" || d.Rule == "" || d.Mode != m {
		t.Fatalf("%+v in %s: %+v, want %s with reason, rule and mode", a, m, d, want)
	}
}

func TestRulesFirstMatchByTable(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ kind, target, want string }{
		{"read", "src/a.go", Allow}, {"read", ".env", Deny},
		{"write", ".duolog/bus.ndjson", Deny}, {"exec", "git push", Deny},
		{"exec", "go test ./...", Allow}, {"net", "example.com", Ask},
	} {
		t.Run(tc.target, func(t *testing.T) {
			check(t, p, LeadRules{}, mode.Edits, Action{tc.kind, tc.target, "implement"}, tc.want)
		})
	}
	// A later deny takes priority, but allow and ask keep their table order.
	p.Rules = []config.Rule{{Action: "exec", Match: "*", Decision: Ask}, {Action: "exec", Match: "*", Decision: Allow}}
	check(t, p, LeadRules{}, mode.Edits, Action{"exec", "go test ./...", "implement"}, Ask)
}

func TestDenyBeatsAllow(t *testing.T) {
	p := policy(t)
	check(t, p, LeadRules{Deny: []config.Rule{{Action: "write", Match: "src/**"}}}, mode.Edits, Action{"write", "src/a.go", "implement"}, Deny)
}
func TestLeadDenyCannotBeRelaxed(t *testing.T) {
	p := policy(t)
	p.InheritLead = false
	check(t, p, LeadRules{Deny: []config.Rule{{Action: "exec", Match: "go *"}}}, mode.Full, Action{"exec", "go test ./...", "implement"}, Deny)
}
func TestFullModeConfirmsUnmatched(t *testing.T) {
	p := policy(t)
	for _, kind := range []string{"exec", "net"} {
		check(t, p, LeadRules{}, mode.Full, Action{kind, "example.com", "implement"}, Allow)
	}
}
func TestFullModeKeepsDenies(t *testing.T) {
	p := policy(t)
	for _, a := range []Action{{"exec", "git push", "implement"}, {"read", ".env", "implement"}, {"write", ".duolog/x", "implement"}, {"write", "src/a.go", "implement"}} {
		check(t, p, LeadRules{Deny: []config.Rule{{Action: "write", Match: "src/**"}}}, mode.Full, a, Deny)
	}
}
func TestFullModeKeepsRoleLimit(t *testing.T) {
	check(t, policy(t), LeadRules{}, mode.Full, Action{"write", "src/a.go", "review_final"}, Deny)
}
func TestPlanModeOnlyReads(t *testing.T) {
	p := policy(t)
	check(t, p, LeadRules{}, mode.Plan, Action{"read", "src/a.go", "implement"}, Allow)
	for _, a := range []Action{{"write", "src/a.go", "implement"}, {"exec", "go test ./...", "implement"}} {
		check(t, p, LeadRules{}, mode.Plan, a, Deny)
	}
}
func TestEditsModeAllowsWrite(t *testing.T) {
	p := policy(t)
	check(t, p, LeadRules{}, mode.Edits, Action{"write", "src/a.go", "implement"}, Allow)
	check(t, p, LeadRules{}, mode.Edits, Action{"exec", "custom-command", "implement"}, Ask)
	check(t, p, LeadRules{}, mode.Ask, Action{"write", "src/a.go", "implement"}, Ask)
}
func TestRoleScopedWrite(t *testing.T) {
	check(t, policy(t), LeadRules{}, mode.Edits, Action{"write", "src/a.go", "review_final"}, Deny)
}
func TestPathTraversalNormalized(t *testing.T) {
	p := policy(t)
	for _, target := range []string{"src/../.duolog/x", ".duolog", "./.duolog/x", `src\..\.duolog\x`} {
		check(t, p, LeadRules{}, mode.Full, Action{"write", target, "implement"}, Deny)
	}
	for _, target := range []string{"src/../.env", "secret.pem", "a/b/key.key", "secrets/a"} {
		check(t, p, LeadRules{}, mode.Full, Action{"read", target, "implement"}, Deny)
	}
}
func TestCompoundShellCommand(t *testing.T) {
	p := policy(t)
	for _, separator := range []string{" && ", " || ", "; ", " | ", "\n"} {
		check(t, p, LeadRules{}, mode.Full, Action{"exec", "go test ./..." + separator + "git push", "implement"}, Deny)
	}
	for _, target := range []string{"go test $(echo ./...)", "go test `echo ./...`", "go test > .duolog/x", "go test &"} {
		check(t, p, LeadRules{}, mode.Full, Action{"exec", target, "implement"}, Ask)
	}
	check(t, p, LeadRules{}, mode.Edits, Action{"exec", "go test ./... && git status", "implement"}, Allow)
	check(t, p, LeadRules{}, mode.Edits, Action{"exec", `go test -run 'a|b' ./...`, "implement"}, Ask)
}

func TestShellDenyBypasses(t *testing.T) {
	p := policy(t)
	for _, inherited := range []bool{false, true} {
		shellPolicy := p
		lead := LeadRules{}
		if inherited {
			lead.Deny = []config.Rule{{Action: "exec", Match: "git push*", Decision: Deny}}
			shellPolicy.Rules = nil
			for _, rule := range p.Rules {
				if rule.Action != "exec" || rule.Decision != Deny {
					shellPolicy.Rules = append(shellPolicy.Rules, rule)
				}
			}
		}
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			for _, tc := range []struct{ command, want string }{
				{`git "push"`, Deny}, {`git 'push'`, Deny}, {`git $'push'`, Ask},
				{`git $operation`, Ask}, {`git ${operation}`, Ask},
				{"git `echo push`", Ask}, {`git $(echo push)`, Ask},
				{`g\it push`, Deny}, {`FOO=1 git push`, Ask},
				{`/usr/bin/git push`, Deny},
				{`go test ./... > /tmp/duolog-out`, Ask},
				{`go test ./... > ../duolog-out`, Ask},
				{`go test ./... && git "push"`, Deny},
				{`git "status" && /usr/bin/git push`, Deny},
				{`/usr/bin/g*t push`, Ask}, {`gi? push`, Ask}, {`[g]it push`, Ask},
				{`go test -run "Test.*" ./...`, Ask},
			} {
				t.Run(string(m)+"/"+tc.command+"/inherited="+strconv.FormatBool(inherited), func(t *testing.T) {
					check(t, shellPolicy, lead, m, Action{"exec", tc.command, "implement"}, tc.want)
				})
			}
			for _, wrapper := range []string{"command", "env", "sudo", "xargs", "eval", "exec", "sh", "bash", "zsh", "pwsh", "cmd"} {
				for _, prefix := range []string{"", "/usr/bin/"} {
					command := prefix + wrapper + ` -c "git push"`
					check(t, shellPolicy, lead, m, Action{"exec", command, "implement"}, Ask)
					check(t, shellPolicy, lead, m, Action{"exec", prefix + wrapper + " git push", "implement"}, Ask)
				}
			}
		}
	}
}

func TestPathSpecificCommandDenies(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		for _, pattern := range []string{"/usr/bin/git push", "/usr/bin/git push*", "git push*"} {
			for _, command := range []string{
				"/usr/bin/git push", "  /usr/bin/git\tpush  ",
				"go test ./... && /usr/bin/git push",
				"/usr/bin/git push || git status",
				`git "status"; /usr/bin/git push`,
			} {
				t.Run("inherited="+strconv.FormatBool(inherited)+"/"+pattern+"/"+command, func(t *testing.T) {
					p := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{{Action: "exec", Match: "*", Decision: Allow}}}
					lead := LeadRules{}
					rule := config.Rule{Action: "exec", Match: pattern, Decision: Deny}
					if inherited {
						lead.Deny = []config.Rule{rule}
					} else {
						p.Rules = append(p.Rules, rule)
					}
					check(t, p, lead, mode.Full, Action{"exec", command, "implement"}, Deny)
					// Path-specific denies must not block other executable paths.
					if strings.HasPrefix(pattern, "/usr/bin/") {
						check(t, p, lead, mode.Full, Action{"exec", "/opt/bin/git push", "implement"}, Allow)
					}
				})
			}
		}
	}
}

func TestPlainDefaultShellAllows(t *testing.T) {
	p := policy(t)
	for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
		for _, command := range []string{
			"go test ./...", "cargo test", "npm run test", "pnpm lint", "uv run test",
			"task build", "make fmt", "git status", "git diff", "git log", "git show",
			"go test ./... && git status",
		} {
			check(t, p, LeadRules{}, m, Action{"exec", command, "implement"}, Allow)
		}
	}
}
func TestEveryAutoDecisionLogged(t *testing.T) {
	p := policy(t)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Action{{"read", "src/a.go", "implement"}, {"exec", "git push", "implement"}, {"net", "example.com", "implement"}} {
		d := Evaluate(p, LeadRules{}, mode.Edits, a)
		if err := Log(s, a, d); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "decisions.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatal(string(data))
	}
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"action", "mode", "decision", "rule", "reason"} {
			if row[key] == nil || row[key] == "" {
				t.Fatalf("missing %s in %s", key, line)
			}
		}
		if action, ok := row["action"].(map[string]any); !ok || action["kind"] == nil || action["target"] == nil || action["role"] != "implement" {
			t.Fatalf("incomplete action in %s", line)
		}
	}
}
func TestImportLeadFollowsLeadAgent(t *testing.T) {
	policy(t)
	root := t.TempDir()
	writeSetting(t, filepath.Join(root, ".claude/settings.json"), `broken`)
	writeSetting(t, filepath.Join(root, ".codex/config.toml"), "sandbox_mode = 'read-only'\napproval_policy = 'never'\n")
	lead, err := ImportLead(root, "codex")
	if err != nil {
		t.Fatal(err)
	}
	d := Evaluate(policy(t), lead, mode.Full, Action{"write", "src/a.go", "implement"})
	if d.Kind != Deny {
		t.Fatal(d)
	}
}
func writeSetting(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestImportClaudeProjectAndUserDenies(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	writeSetting(t, filepath.Join(os.Getenv("HOME"), ".claude/settings.json"), `{"permissions":{"deny":["Bash(git push:*)", "Read(./private/**)"]}}`)
	writeSetting(t, filepath.Join(root, ".claude/settings.json"), `{"permissions":{"deny":["Write(./src/**)"]}}`)
	lead, err := ImportLead(root, "claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Action{{"exec", "git push origin main", "implement"}, {"read", "private/x", "implement"}, {"write", "src/a.go", "implement"}} {
		check(t, p, lead, mode.Full, a, Deny)
	}
	writeSetting(t, filepath.Join(root, ".claude/settings.json"), `{"permissions":{"deny":"invalid"}}`)
	if _, err := ImportLead(root, "claude"); err == nil {
		t.Fatal("malformed settings accepted")
	}
}

func TestImportClaudePathSelectorBases(t *testing.T) {
	p := policy(t)
	home := os.Getenv("HOME")
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "restricted")
	for _, source := range []string{
		filepath.Join(home, ".claude/settings.json"),
		filepath.Join(root, ".claude/settings.json"),
		filepath.Join(root, ".claude/settings.local.json"),
	} {
		t.Run(source, func(t *testing.T) {
			base := root
			if source == filepath.Join(home, ".claude/settings.json") {
				base = filepath.Join(home, ".claude")
			}
			for _, tc := range []struct{ selector, path string }{
				{"/restricted/**", filepath.Join(base, "restricted", "file.txt")},
				{"~/restricted/**", filepath.Join(home, "restricted", "file.txt")},
				{"/" + filepath.ToSlash(outside) + "/**", filepath.Join(outside, "file.txt")},
				{"./restricted/**", filepath.Join(root, "restricted", "file.txt")},
				{"restricted/**", filepath.Join(root, "restricted", "file.txt")},
			} {
				for _, tool := range []string{"Read", "Edit"} {
					entry := tool + "(" + tc.selector + ")"
					data, err := json.Marshal(map[string]any{"permissions": map[string]any{"deny": []string{entry}}})
					if err != nil {
						t.Fatal(err)
					}
					writeSetting(t, source, string(data))
					lead, err := ImportLead(root, "claude")
					if err != nil {
						t.Fatal(err)
					}
					target, err := filepath.Rel(root, tc.path)
					if err != nil {
						t.Fatal(err)
					}
					kind := "read"
					if tool == "Edit" {
						kind = "write"
					}
					t.Run(entry, func(t *testing.T) {
						check(t, p, lead, mode.Full, Action{kind, filepath.ToSlash(target), "implement"}, Deny)
						check(t, p, lead, mode.Full, Action{kind, "public/file.txt", "implement"}, Allow)
					})
				}
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestImportCodexLimits(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	writeSetting(t, filepath.Join(os.Getenv("HOME"), ".codex/config.toml"), "sandbox_mode = 'read-only'\n")
	writeSetting(t, filepath.Join(root, ".codex/config.toml"), "sandbox_mode = 'workspace-write'\napproval_policy = 'never'\n")
	lead, err := ImportLead(root, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if d := Evaluate(p, lead, mode.Full, Action{"write", "src/a.go", "implement"}); d.Kind != Deny {
		t.Fatal(d)
	}
	writeSetting(t, filepath.Join(root, ".codex/config.toml"), "sandbox_mode = [\n")
	if _, err := ImportLead(root, "codex"); err == nil {
		t.Fatal("malformed TOML accepted")
	}
}

func TestMalformedLeadDeniesFailClosed(t *testing.T) {
	policy(t)
	root := t.TempDir()
	for _, data := range []string{
		`null`, `{"permissions":null}`, `{"permissions":{"deny":null}}`,
		`{"permissions":{"deny":[null]}}`, `{"permissions":{"deny":["Read([)"]}}`,
	} {
		writeSetting(t, filepath.Join(root, ".claude/settings.json"), data)
		if _, err := ImportLead(root, "claude"); err == nil {
			t.Errorf("accepted malformed settings: %s", data)
		}
	}
}

func TestMalformedPolicyFailsClosed(t *testing.T) {
	p := policy(t)
	p.Rules = append(p.Rules, config.Rule{Action: "read", Match: "[", Decision: Deny})
	check(t, p, LeadRules{}, mode.Full, Action{"read", "src/a.go", "implement"}, Deny)
}

func TestUnverifiedShellSyntaxNeedsApproval(t *testing.T) {
	p := policy(t)
	for _, command := range []string{`go test ./... && g\it push`, "go test ./... && git\tpush", "go test ./... && git  push"} {
		if d := Evaluate(p, LeadRules{}, mode.Full, Action{"exec", command, "implement"}); d.Kind == Allow {
			t.Errorf("allowed %q: %+v", command, d)
		}
	}
}
func BenchmarkGate(b *testing.B) {
	root := b.TempDir()
	b.Setenv("HOME", root)
	b.Setenv("USERPROFILE", root)
	b.Setenv("XDG_CONFIG_HOME", root)
	b.Setenv("APPDATA", root)
	b.ResetTimer()
	for b.Loop() {
		c, err := config.Load(root)
		if err != nil {
			b.Fatal(err)
		}
		lead, err := ImportLead(root, "codex")
		if err != nil {
			b.Fatal(err)
		}
		d := Evaluate(c.Policy, lead, mode.Edits, Action{"exec", "go test ./...", "implement"})
		if d.Kind != Allow {
			b.Fatal(d)
		}
	}
}
