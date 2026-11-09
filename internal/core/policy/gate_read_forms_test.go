package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// readFormCases are read-only forms a measured planner worker ran. Each is
// judged for a read-only role and a write role.
func TestReadFormsAllowed(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, role := range []string{"planner", "implement", "review"} {
		for _, command := range []string{
			"rg -n -e Alpha -e Beta internal docs",
			"rg -n -e Alpha -e Beta internal docs | head -80",
			"cmp internal/a.go internal/b.go",
		} {
			a := Action{Kind: "exec", Target: command, Role: role, Work: root, Cwd: root, State: root}
			if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind != Allow {
				t.Errorf("%s %q: %+v, want allow", role, command, d)
			}
		}
	}
}

// What stays refused or asked: reaching hidden state, running programs,
// reading secrets, and paths or directories outside the work root.
func TestReadFormsStillRefused(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	outside := absoluteTestPath(root, "/elsewhere")
	for _, role := range []string{"planner", "implement", "review"} {
		for _, command := range []string{
			"rg --files -g '*duolog*'",
			"rg --files -g '**/.duolog/**'",
			"rg --files --glob='*duolog*'",
			"rg --files --hidden internal docs",
			"git -C . status --short",
			"git -C internal status --short",
			"rg --files --hidden -g '*duolog*'",
			"rg --files -. -g '*duolog*'",
			"rg --files -u -g '*duolog*'",
			"rg --files -uu -g '*duolog*'",
			"rg --files -uuu -g '*duolog*'",
			"rg --files --no-ignore -g '*duolog*'",
			"rg --files --no-ignore-vcs -g '*duolog*'",
			"rg --files --unrestricted -g '*duolog*'",
			"rg -n -g '*duolog*' x .",
			"rg --files -g '.duolog' .duolog",
			"rg --files -g '*duolog*' .duolog",
			"rg --files -g '*duolog*' .duolog/tasks",
			"rg --files --hidden .duolog",
			"rg --files --hidden -g '*duolog*' .",
			"rg --files --hidden .env",
			"rg --files --hidden internal/.duolog",
			"rg --files --hidden -u internal",
			"rg --files --hidden --no-ignore internal",
			"rg --files --hidden -g '*.go' internal",
			"rg --hidden -e A internal",
			"rg --files --hidden",
			"git -C internal/.duolog status --short",
			"git -C .duolog/tasks status --short",
			"git -C internal status",
			"git -C internal status --short -- .env",
			"git -c core.pager=x status --short",
			"rg -n -e A -e B .duolog",
			"rg --pre cat -n x internal",
			"rg --pre=cat -n x internal",
			"rg --files --hidden " + outside,
			"rg -n -e A -e B " + outside,
			"rg -n -e A -e B ../elsewhere",
			"rg -n -e A -e B .env",
			"cmp .env x",
			"cmp x .env",
			"cmp x " + outside,
			"cmp .duolog/state x",
			"cmp -l x y",
			"git -C " + outside + " status --short",
			"git -C .. status --short",
			"git -C ../elsewhere status --short",
			"git -C .duolog status --short",
			"git -C internal commit -m x",
			"printenv",
			"python3 -c print(1)",
		} {
			a := Action{Kind: "exec", Target: command, Role: role, Work: root, Cwd: root, State: root}
			if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind == Allow {
				t.Errorf("%s %q: %+v, want deny or ask", role, command, d)
			}
		}
	}
}
