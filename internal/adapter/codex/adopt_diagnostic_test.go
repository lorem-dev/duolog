package codex

import (
	"bytes"
	"github.com/lorem-dev/duolog/internal/install"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdoptionProblem kills silent skips, wrong causes, and diagnostic trust writes.
func TestAdoptionProblem(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"trust", "source checkout lacks folder trust"},
		{"approval", "source checkout lacks gate approval"},
		// The gate approval is the blocker, so a difference in another hook
		// must not hide it. Mutation killed: let the hook comparison run when
		// the gate approval is already known to be missing.
		{"approval with another hook changed", "source checkout lacks gate approval"},
		{"stale approval", "source checkout has a stale gate approval; renew it in /hooks in the source checkout"},
		{"foreign", "hooks to copy are not duolog's own"},
		{"tree", ".codex/config.toml is not the same"},
		{"explicit trust", "worktree has an explicit trust level"},
		{"ready", ""},
		{"target approval conflict", "target checkout already has a different gate approval"},
		{"nothing to copy", "cause is unknown"},
		{"unreadable source hooks", "cause is unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := adoptHome(t)
			root, dir := t.TempDir(), t.TempDir()
			hooks, err := HooksJSON(install.GateTimeout)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "foreign" {
				hooks = []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"other-gate"}]}]}}`)
			}
			writeFile(t, filepath.Join(root, ".codex", "hooks.json"), string(hooks))
			writeFile(t, filepath.Join(dir, ".codex", "hooks.json"), string(hooks))
			data := approvalsFor(root)
			if tc.name == "explicit trust" {
				data += "[projects." + quote(dir) + "]\ntrust_level = \"untrusted\"\n"
			}
			if tc.name == "trust" {
				data = strings.Replace(data, `trust_level = "trusted"`, `trust_level = "untrusted"`, 1)
			}
			if tc.name == "approval" {
				data = "[projects." + quote(root) + "]\ntrust_level = \"trusted\"\n"
			}
			if tc.name == "approval with another hook changed" {
				var kept []string
				for _, table := range strings.Split(data, "\n[") {
					if !strings.Contains(table, ":pre_tool_use:0:0") {
						kept = append(kept, table)
					}
				}
				data = strings.Join(kept, "\n[")
				changed := strings.Replace(string(hooks), "hook SessionStart", "hook SessionStart --changed", 1)
				writeFile(t, filepath.Join(dir, ".codex", "hooks.json"), changed)
			}
			if tc.name == "target approval conflict" {
				data += "[hooks.state." + quote(filepath.Join(dir, ".codex", "hooks.json")+":pre_tool_use:0:0") + "]\ntrusted_hash = \"different-hash\"\n"
			}
			if tc.name == "nothing to copy" {
				data += approvalsFor(dir)
			}
			if tc.name == "unreadable source hooks" {
				writeFile(t, filepath.Join(root, ".codex", "hooks.json"), "invalid JSON")
			}
			if tc.name == "tree" {
				writeFile(t, filepath.Join(root, ".codex", "config.toml"), "model = 'a'\n")
				writeFile(t, filepath.Join(dir, ".codex", "config.toml"), "model = 'b'\n")
				writeFile(t, filepath.Join(dir, ".codex", "z.rules"), "different\n")
			}
			path := filepath.Join(home, "config.toml")
			writeFile(t, path, data)
			a := adopter{path: path, dir: dir, roots: []string{root}, userHooks: filepath.Join(home, "hooks.json")}
			if tc.name == "stale approval" {
				a.stale = map[string]string{filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:0:0": fixtureApprovalHash("PreToolUse")}
			}
			got := a.adoptionProblem([]byte(data))
			if tc.want == "" {
				if got != "" {
					t.Fatalf("unexpected problem: %s", got)
				}
			} else if !strings.Contains(got, tc.want) || !strings.Contains(got, root) {
				t.Fatalf("problem=%q; want %q and source folder", got, tc.want)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, []byte(data)) {
				t.Fatalf("diagnostic changed config: %s, %v", after, err)
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 1 {
				t.Fatalf("diagnostic created files: %v, %v", entries, err)
			}
		})
	}
}
