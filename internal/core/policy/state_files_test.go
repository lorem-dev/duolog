package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// stateFixture is a work root whose .duolog folder holds a brief, a log, a
// symlink with a Markdown name that leads to the log, a folder with a
// Markdown name and a symlinked folder that leads to the brief's folder.
func stateFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	work := t.TempDir()
	state := filepath.Join(work, ".duolog")
	for _, dir := range []string{"s5", "dir.md"} {
		if err := os.MkdirAll(filepath.Join(state, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"s5/w0-contracts.md": "Task S5-6\n", "bus.ndjson": "{}\n"} {
		if err := os.WriteFile(filepath.Join(state, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, to := range map[string]string{"link.md": "bus.ndjson", "linked": "s5"} {
		if err := os.Symlink(to, filepath.Join(state, link)); err != nil {
			t.Fatal(err)
		}
	}
	return work
}

// The Read tool, Grep on the file, cat, sed and rg agree on every path under
// .duolog: a plain file the state rule allows is readable, and a state file,
// a symlink anywhere below .duolog, a folder and a glob are refused.
func TestStateFileReadsAgree(t *testing.T) {
	p := policy(t)
	work := stateFixture(t)
	tool := func(target string, search bool) Action {
		return Action{Kind: "read", Target: target, Role: "implement", Work: work, Search: search}
	}
	exec := func(command string) Action {
		return Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work}
	}
	for _, role := range []string{"implement", "review"} {
		for _, target := range []string{".duolog/s5/w0-contracts.md", filepath.Join(work, ".duolog/s5/w0-contracts.md")} {
			// The hook makes a read tool's target relative to the work root.
			if !filepath.IsAbs(target) {
				for _, a := range []Action{tool(target, false), tool(target, true)} {
					a.Role = role
					check(t, p, LeadRules{}, mode.Full, a, Allow)
				}
			}
			for _, command := range []string{
				`rg -n -F "Task S5-6" -A 60 ` + target, "cat " + target, "sed -n 1,5p " + target,
				"grep -n Task " + target,
			} {
				a := exec(command)
				a.Role = role
				check(t, p, LeadRules{}, mode.Full, a, Allow)
			}
		}
	}
	for _, target := range []string{".duolog/bus.ndjson", ".duolog/link.md", ".duolog/linked/w0-contracts.md", ".duolog/dir.md"} {
		for _, a := range []Action{tool(target, false), tool(target, true), exec("rg Task " + target), exec("cat " + target), exec("sed -n 1,5p " + target)} {
			d := Evaluate(p, LeadRules{}, mode.Full, a)
			if d.Kind != Deny || d.Rule != RuleState {
				t.Errorf("%+v: %+v, want a state deny", a, d)
			}
		}
	}
	for _, a := range []Action{
		tool(".duolog/s5", true), exec("rg Task .duolog/s5"), exec("rg Task .duolog/s5/*.md"),
		exec("grep -r Task .duolog/s5"), exec("rg -g '.duolog' Task .duolog/s5/w0-contracts.md"),
	} {
		check(t, p, LeadRules{}, mode.Full, a, Deny)
	}
}

func TestStateFile(t *testing.T) {
	work := stateFixture(t)
	for target, want := range map[string]stateEntry{
		".duolog/s5/w0-contracts.md":           statePlain,
		"s5/w0-contracts.md":                   stateMissing,
		".duolog":                              stateMissing,
		".duolog/":                             stateMissing,
		".duolog/missing.md":                   stateMissing,
		".duolog/link.md":                      stateOther,
		".duolog/linked/w0-contracts.md":       stateOther,
		".duolog/dir.md":                       stateOther,
		".duolog/s5/../s5/w0-contracts.md":     stateOther,
		".duolog/linked/../bus.ndjson":         stateOther,
		".duolog/../s5":                        stateMissing,
		".duolog/s5/*.md":                      stateOther,
		".duolog/s5/w0-contracts.md/x":         stateOther,
		filepath.Join(work, ".duolog/link.md"): stateOther,
	} {
		if got := stateFile(work, work, target); got != want {
			t.Errorf("%s: %v, want %v", target, got, want)
		}
	}
	if stateFile("", "", ".duolog/link.md") != stateMissing {
		t.Fatal("a target with no work root cannot be looked up")
	}
}
