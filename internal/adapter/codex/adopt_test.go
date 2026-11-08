package codex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/install"
)

// adoptHome points HOME and CODEX_HOME at a temporary folder, never the
// person's own, and returns the Codex home.
func adoptHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	return codexHome
}

// git runs git in dir without the caller's Git variables or config.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=duolog", "-c", "user.email=duolog@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main", "-c", "core.autocrlf=false"}, args...)...)
	cmd.Env = append(gitEnv(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// repoWithHooks makes a repository at dir whose committed .codex/hooks.json
// holds duolog's Codex hooks.
func repoWithHooks(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	data, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".codex", "hooks.json"), string(data))
	git(t, dir, "add", ".codex/hooks.json")
	git(t, dir, "commit", "-q", "-m", "hooks")
}

// approvalsFor is the config.toml text that trusts root and approves every
// duolog hook in its hooks file, each with an invented hash.
func approvalsFor(root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[projects.%s]\ntrust_level = \"trusted\"\n", quote(root))
	path := filepath.Join(root, ".codex", "hooks.json")
	for _, e := range events(install.GateTimeout) {
		snake := ""
		for spelling, name := range codexEvents() {
			if name == e.Name && spelling != name {
				snake = spelling
			}
		}
		fmt.Fprintf(&b, "\n[hooks.state.%s]\ntrusted_hash = %s\n", quote(path+":"+snake+":0:0"), quote("sha256:"+e.Name))
	}
	return b.String()
}

const configHead = `# the person's own comment
model = "gpt-5"
approval_policy = "on-request"
sandbox_mode = "workspace-write"

[projects."/elsewhere"]
trust_level = "untrusted"

[hooks.state."/elsewhere/.codex/hooks.json:pre_tool_use:0:0"]
trusted_hash = "sha256:other"
`

type adoptFixture struct {
	codexHome, lead, worktree, config string
}

// newAdoptFixture is a lead repository that Codex trusts with every duolog
// hook approved, and a linked worktree of it under .worktrees.
func newAdoptFixture(t *testing.T) adoptFixture {
	t.Helper()
	f := adoptFixture{codexHome: adoptHome(t), lead: filepath.Join(t.TempDir(), "project")}
	repoWithHooks(t, f.lead)
	f.worktree = filepath.Join(f.lead, ".worktrees", "s5-w0")
	git(t, f.lead, "worktree", "add", "-q", "--detach", f.worktree)
	f.config = filepath.Join(f.codexHome, "config.toml")
	writeFile(t, f.config, configHead+"\n"+approvalsFor(f.lead))
	return f
}

func (f adoptFixture) read(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAdoptWorktreeCopiesTrustAndDuologApprovals(t *testing.T) {
	f := newAdoptFixture(t)
	before := f.read(t)
	if ok, err := folder(f.worktree); err != nil || ok {
		t.Fatalf("worktree trusted before adoption: %v %v", ok, err)
	}
	lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1+len(events(install.GateTimeout)) {
		t.Fatalf("lines = %q", lines)
	}
	after := f.read(t)
	if !bytes.HasPrefix(after, before) {
		t.Fatalf("config changed before its end:\n%s", after)
	}
	if ok, err := folder(f.worktree); err != nil || !ok {
		t.Fatalf("worktree not trusted: %v %v", ok, err)
	}
	if s, err := hooks(f.worktree); err != nil || !s.Trusted || s.Unproven != "" {
		t.Fatalf("worktree hooks = %+v, %v", s, err)
	}
	var cfg adoptConfig
	if err := toml.Unmarshal(after, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, e := range events(install.GateTimeout) {
		found := false
		for key, entry := range cfg.Hooks.State {
			path, _, position, ok := splitApproval(key)
			if ok && position == "0:0" && samePath(path, filepath.Join(f.worktree, ".codex", "hooks.json")) && entry["trusted_hash"] == "sha256:"+e.Name {
				found = true
			}
		}
		if !found {
			t.Errorf("no approval of %s copied", e.Name)
		}
	}
	if backup, err := os.ReadFile(f.config + configBackupSuffix); err != nil || !bytes.Equal(backup, before) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	for _, key := range []string{"approval_policy", "sandbox_mode"} {
		if strings.Count(string(after), key) != 1 {
			t.Errorf("%s written again", key)
		}
	}
	if strings.Contains(string(after), "danger-full-access") {
		t.Fatal("sandbox loosened")
	}
	// A second call finds nothing missing and writes nothing.
	lines, err = Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if err != nil || len(lines) != 0 || !bytes.Equal(f.read(t), after) {
		t.Fatalf("second adoption wrote %q, %v", lines, err)
	}
}

// The lead may itself be a linked worktree that Codex does not trust; the
// main checkout's trust and approvals count then.
func TestAdoptWorktreeFromMainCheckout(t *testing.T) {
	f := newAdoptFixture(t)
	other := filepath.Join(f.lead, ".worktrees", "s5-w1")
	git(t, f.lead, "worktree", "add", "-q", "--detach", other)
	lines, err := Trust{}.AdoptWorktree(f.worktree, other, os.Environ())
	if err != nil || len(lines) == 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	if s, err := hooks(other); err != nil || !s.Trusted || s.Unproven != "" {
		t.Fatalf("hooks = %+v, %v", s, err)
	}
}

func TestAdoptWorktreeWritesNothingWhenACheckFails(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *adoptFixture) (lead, dir string)
	}{
		{"untrusted lead root", func(t *testing.T, f *adoptFixture) (string, string) {
			writeFile(t, f.config, configHead+"\n"+strings.Replace(approvalsFor(f.lead), `trust_level = "trusted"`, `trust_level = "untrusted"`, 1))
			return f.lead, f.worktree
		}},
		{"hooks not approved", func(t *testing.T, f *adoptFixture) (string, string) {
			writeFile(t, f.config, configHead+fmt.Sprintf("\n[projects.%s]\ntrust_level = \"trusted\"\n", quote(f.lead)))
			return f.lead, f.worktree
		}},
		{"approved gate is not duolog's", func(t *testing.T, f *adoptFixture) (string, string) {
			foreign := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"my-gate"}]}]}}`
			writeFile(t, filepath.Join(f.lead, ".codex", "hooks.json"), foreign)
			writeFile(t, filepath.Join(f.worktree, ".codex", "hooks.json"), foreign)
			return f.lead, f.worktree
		}},
		{"worktree hooks differ", func(t *testing.T, f *adoptFixture) (string, string) {
			writeFile(t, filepath.Join(f.worktree, ".codex", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"my-gate"}]}]}}`)
			return f.lead, f.worktree
		}},
		{"stale gate approval", func(t *testing.T, f *adoptFixture) (string, string) {
			key := filepath.Join(f.lead, ".codex", "hooks.json") + ":pre_tool_use:0:0"
			writeFile(t, filepath.Join(f.codexHome, "duolog-stale-approvals.json"), fmt.Sprintf(`{%q: "sha256:PreToolUse"}`, key))
			return f.lead, f.worktree
		}},
		{"worktree trust set otherwise", func(t *testing.T, f *adoptFixture) (string, string) {
			writeFile(t, f.config, configHead+"\n"+approvalsFor(f.lead)+fmt.Sprintf("\n[projects.%s]\ntrust_level = \"untrusted\"\n", quote(f.worktree)))
			return f.lead, f.worktree
		}},
		{"projects as an inline table", func(t *testing.T, f *adoptFixture) (string, string) {
			body := strings.Replace(approvalsFor(f.lead), fmt.Sprintf("[projects.%s]\ntrust_level = \"trusted\"\n", quote(f.lead)), "", 1)
			writeFile(t, f.config, fmt.Sprintf("projects = { %s = { trust_level = \"trusted\" } }\n", quote(f.lead))+body)
			return f.lead, f.worktree
		}},
		{"another repository", func(t *testing.T, f *adoptFixture) (string, string) {
			other := filepath.Join(t.TempDir(), "other")
			repoWithHooks(t, other)
			return f.lead, other
		}},
		{"worktree of another repository", func(t *testing.T, f *adoptFixture) (string, string) {
			other := filepath.Join(t.TempDir(), "other")
			repoWithHooks(t, other)
			wt := filepath.Join(other, ".worktrees", "w")
			git(t, other, "worktree", "add", "-q", "--detach", wt)
			return f.lead, wt
		}},
		{"planted .git file", func(t *testing.T, f *adoptFixture) (string, string) {
			planted := filepath.Join(t.TempDir(), "planted")
			data, _ := os.ReadFile(filepath.Join(f.worktree, ".git"))
			writeFile(t, filepath.Join(planted, ".git"), string(data))
			hooksData, _ := os.ReadFile(filepath.Join(f.worktree, ".codex", "hooks.json"))
			writeFile(t, filepath.Join(planted, ".codex", "hooks.json"), string(hooksData))
			return f.lead, planted
		}},
		{"prunable entry", func(t *testing.T, f *adoptFixture) (string, string) {
			moved := filepath.Join(t.TempDir(), "moved")
			if err := os.Rename(f.worktree, moved); err != nil {
				t.Fatal(err)
			}
			return f.lead, moved
		}},
		{"main checkout as the worktree", func(t *testing.T, f *adoptFixture) (string, string) {
			writeFile(t, f.config, configHead+"\n"+approvalsFor(f.worktree))
			return f.worktree, f.lead
		}},
		{"subfolder of the worktree", func(t *testing.T, f *adoptFixture) (string, string) {
			sub := filepath.Join(f.worktree, ".codex")
			return f.lead, sub
		}},
		{"inherited GIT_DIR", func(t *testing.T, f *adoptFixture) (string, string) {
			// The trusted lead is an unrelated repository with the same
			// hooks; GIT_DIR points git at the repository the worktree
			// belongs to, whose list would name it.
			lead := filepath.Join(t.TempDir(), "lead")
			repoWithHooks(t, lead)
			writeFile(t, f.config, configHead+"\n"+approvalsFor(lead))
			t.Setenv("GIT_DIR", filepath.Join(f.lead, ".git"))
			return lead, f.worktree
		}},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct {
			name  string
			setup func(t *testing.T, f *adoptFixture) (lead, dir string)
		}{"path with a newline", func(t *testing.T, f *adoptFixture) (string, string) {
			wt := filepath.Join(f.lead, ".worktrees", "a\nworktree "+f.worktree)
			git(t, f.lead, "worktree", "add", "-q", "--detach", wt)
			return f.lead, wt
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdoptFixture(t)
			lead, dir := tc.setup(t, &f)
			before := f.read(t)
			lines, err := Trust{}.AdoptWorktree(lead, dir, os.Environ())
			if len(lines) != 0 || err != nil {
				t.Fatalf("wrote %q, %v", lines, err)
			}
			if !bytes.Equal(f.read(t), before) {
				t.Fatalf("config changed:\n%s", f.read(t))
			}
			if _, err := os.Stat(f.config + configBackupSuffix); !os.IsNotExist(err) {
				t.Fatalf("backup written: %v", err)
			}
		})
	}
}

// Trust makes Codex load the worktree's own .codex/config.toml and
// .codex/rules, which its branch may change: duolog writes trust only when
// they are the same as in the source root, and says why otherwise.
func TestAdoptWorktreeRefusesADifferentCodexFolder(t *testing.T) {
	cases := []struct {
		name, file  string
		lead, other string // "" leaves the file out
	}{
		{"config.toml only in the worktree", "config.toml", "", "[mcp_servers.x]\ncommand = \"x\"\n"},
		{"config.toml differs", "config.toml", "model = \"a\"\n", "model = \"b\"\n"},
		{"config.toml only in the lead", "config.toml", "model = \"a\"\n", ""},
		{"rule only in the worktree", filepath.Join("rules", "x.rules"), "", "allow\n"},
		{"rule differs", filepath.Join("rules", "sub", "x.rules"), "deny\n", "allow\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdoptFixture(t)
			if tc.lead != "" {
				writeFile(t, filepath.Join(f.lead, ".codex", tc.file), tc.lead)
			}
			if tc.other != "" {
				writeFile(t, filepath.Join(f.worktree, ".codex", tc.file), tc.other)
			}
			before := f.read(t)
			lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
			if len(lines) != 0 || err == nil || !strings.Contains(err.Error(), filepath.ToSlash(tc.file)) {
				t.Fatalf("lines = %q, err = %v", lines, err)
			}
			if !bytes.Equal(f.read(t), before) {
				t.Fatalf("config changed:\n%s", f.read(t))
			}
			if _, err := os.Stat(f.config + configBackupSuffix); !os.IsNotExist(err) {
				t.Fatalf("backup written: %v", err)
			}
		})
	}
	t.Run("the same in both", func(t *testing.T) {
		f := newAdoptFixture(t)
		for _, dir := range []string{f.lead, f.worktree} {
			writeFile(t, filepath.Join(dir, ".codex", "config.toml"), "model = \"a\"\n")
			writeFile(t, filepath.Join(dir, ".codex", "rules", "x.rules"), "allow\n")
		}
		lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
		if err != nil || len(lines) == 0 {
			t.Fatalf("lines = %q, %v", lines, err)
		}
	})
}

// adoptRefused checks that adopting f's worktree wrote nothing and named want.
func adoptRefused(t *testing.T, f adoptFixture, want string) {
	t.Helper()
	before := f.read(t)
	lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if len(lines) != 0 || err == nil || !strings.Contains(err.Error(), want+" is not the same") {
		t.Fatalf("lines = %q, err = %v, want %s named", lines, err, want)
	}
	if !bytes.Equal(f.read(t), before) {
		t.Fatalf("config changed:\n%s", f.read(t))
	}
	if _, err := os.Stat(f.config + configBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("backup written: %v", err)
	}
}

// The whole .codex tree but hooks.json counts: a file that the same
// config.toml names, a name only one tree has, and anything but a plain file
// or folder in either tree differ. The error names the path that differs.
func TestAdoptWorktreeComparesTheWholeCodexTree(t *testing.T) {
	type treeCase struct {
		name, want string
		setup      func(t *testing.T, f adoptFixture)
	}
	cases := []treeCase{
		{"a file the config names differs", ".codex/agents/r.toml", func(t *testing.T, f adoptFixture) {
			for _, dir := range []string{f.lead, f.worktree} {
				writeFile(t, filepath.Join(dir, ".codex", "config.toml"), "[agents.r]\nconfig_file = \"agents/r.toml\"\n")
				writeFile(t, filepath.Join(dir, ".codex", "agents", "r.toml"), "model = \"a\"\n")
			}
			writeFile(t, filepath.Join(f.worktree, ".codex", "agents", "r.toml"), "[mcp_servers.x]\ncommand = \"evil\"\n")
		}},
		{"a file only in the worktree", ".codex/prompts/x.md", func(t *testing.T, f adoptFixture) {
			writeFile(t, filepath.Join(f.worktree, ".codex", "prompts", "x.md"), "hi\n")
		}},
		{"a folder only in the worktree", ".codex/skills", func(t *testing.T, f adoptFixture) {
			if err := os.MkdirAll(filepath.Join(f.worktree, ".codex", "skills"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases,
			treeCase{"a link in both", ".codex/agents/r.toml", func(t *testing.T, f adoptFixture) {
				target := filepath.Join(t.TempDir(), "r.toml")
				writeFile(t, target, "model = \"a\"\n")
				for _, dir := range []string{f.lead, f.worktree} {
					if err := os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, filepath.Join(dir, ".codex", "agents", "r.toml")); err != nil {
						t.Fatal(err)
					}
				}
			}},
			treeCase{"a linked config.toml", ".codex/config.toml", func(t *testing.T, f adoptFixture) {
				writeFile(t, filepath.Join(f.lead, ".codex", "config.toml"), "model = \"a\"\n")
				target := filepath.Join(t.TempDir(), "config.toml")
				writeFile(t, target, "model = \"a\"\n")
				if err := os.Symlink(target, filepath.Join(f.worktree, ".codex", "config.toml")); err != nil {
					t.Fatal(err)
				}
			}},
			treeCase{"a linked .codex", ".codex", func(t *testing.T, f adoptFixture) {
				copied := filepath.Join(t.TempDir(), "codex")
				data, err := os.ReadFile(filepath.Join(f.worktree, ".codex", "hooks.json"))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(copied, "hooks.json"), string(data))
				if err := os.RemoveAll(filepath.Join(f.worktree, ".codex")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(copied, filepath.Join(f.worktree, ".codex")); err != nil {
					t.Fatal(err)
				}
			}},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdoptFixture(t)
			tc.setup(t, f)
			adoptRefused(t, f, tc.want)
		})
	}
}

// A trust duolog wrote is checked again on each use. Once the worktree's
// .codex tree differs, duolog takes out exactly the tables it wrote, says
// why, and the agent does not run there; a trust the person wrote stays.
func TestAdoptWorktreeWithdrawsItsTrustWhenTheTreeChanges(t *testing.T) {
	f := newAdoptFixture(t)
	before := f.read(t)
	if lines, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil || len(lines) == 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	// The same tree: nothing to do.
	if lines, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil || len(lines) != 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	// Codex wrote a table after duolog's meanwhile; it stays.
	codexTable := "\n[projects.\"/codex-wrote-this\"]\ntrust_level = \"trusted\"\n"
	writeFile(t, f.config, string(f.read(t))+codexTable)
	writeFile(t, filepath.Join(f.worktree, ".codex", "config.toml"), "[mcp_servers.x]\ncommand = \"evil\"\n")
	lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if !errors.Is(err, agent.ErrWorktreeChanged) || !strings.Contains(err.Error(), ".codex/config.toml") {
		t.Fatalf("err = %v", err)
	}
	if len(lines) != 1+len(events(install.GateTimeout)) || !strings.Contains(lines[0], "no longer trusts") {
		t.Fatalf("lines = %q", lines)
	}
	want := string(before) + codexTable
	if got := string(f.read(t)); got != want {
		t.Fatalf("config =\n%s\nwant\n%s", got, want)
	}
	if ok, err := folder(f.worktree); err != nil || ok {
		t.Fatalf("worktree still trusted: %v %v", ok, err)
	}
	// Later uses find no trust of duolog's to withdraw, and give none.
	lines, err = Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if len(lines) != 0 || err == nil || errors.Is(err, agent.ErrWorktreeChanged) || string(f.read(t)) != want {
		t.Fatalf("lines = %q, err = %v", lines, err)
	}
	// The person trusts the worktree themselves: duolog never takes that out.
	writeFile(t, f.config, want+fmt.Sprintf("\n[projects.%s]\ntrust_level = \"trusted\"\n", quote(f.worktree)))
	for range 2 {
		if _, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := folder(f.worktree); err != nil || !ok {
		t.Fatalf("the person's trust was taken out: %v %v\n%s", ok, err, f.read(t))
	}
}

// duolog's block ends in its end marker, and a withdrawal never takes a table
// after it: an approval or trust the person gave the worktree later stays.
func TestAdoptWorktreeWithdrawalKeepsThePersonsTablesAfterItsBlock(t *testing.T) {
	f := newAdoptFixture(t)
	before := f.read(t)
	if lines, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil || len(lines) == 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	adopted := string(f.read(t))
	if !strings.HasSuffix(adopted, "\n"+commentEnd+"\n") {
		t.Fatalf("block does not end in %q:\n%s", commentEnd, adopted)
	}
	hooksFile := filepath.Join(f.worktree, ".codex", "hooks.json")
	persons := fmt.Sprintf("\n[hooks.state.%s]\ntrusted_hash = \"sha256:mine\"\n", quote(hooksFile+":pre_tool_use:1:0")) +
		fmt.Sprintf("\n[hooks.state.%s]\ntrusted_hash = \"sha256:PreToolUse\"\n", quote(hooksFile+":PreToolUse:0:0")) +
		fmt.Sprintf("\n[projects.%s]\ntrust_level = \"trusted\"\n", quote(f.worktree+string(filepath.Separator)+"."))
	writeFile(t, f.config, adopted+persons)
	writeFile(t, filepath.Join(f.worktree, ".codex", "rules", "x.rules"), "allow\n")
	lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if !errors.Is(err, agent.ErrWorktreeChanged) || len(lines) != 1+len(events(install.GateTimeout)) {
		t.Fatalf("lines = %q, err = %v", lines, err)
	}
	if got, want := string(f.read(t)), string(before)+persons; got != want {
		t.Fatalf("config =\n%s\nwant\n%s", got, want)
	}
}

// A table in duolog's block that no longer holds what duolog wrote stops the
// withdrawal there: it and everything after it stay.
func TestAdoptWorktreeWithdrawalStopsAtAChangedTable(t *testing.T) {
	f := newAdoptFixture(t)
	if lines, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil || len(lines) == 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	adopted := string(f.read(t))
	// The last approval in the block no longer has the source root's hash.
	last := strings.LastIndex(adopted, "trusted_hash = ")
	end := last + strings.IndexByte(adopted[last:], '\n')
	changed := adopted[:last] + `trusted_hash = "sha256:changed"` + adopted[end:]
	writeFile(t, f.config, changed)
	writeFile(t, filepath.Join(f.worktree, ".codex", "rules", "x.rules"), "allow\n")
	lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if !errors.Is(err, agent.ErrWorktreeChanged) || len(lines) != len(events(install.GateTimeout)) {
		t.Fatalf("lines = %q, err = %v", lines, err)
	}
	got := string(f.read(t))
	if !strings.Contains(got, "sha256:changed") || !strings.HasSuffix(got, "\n"+commentEnd+"\n") {
		t.Fatalf("the changed table or the end marker was taken:\n%s", got)
	}
	if strings.Contains(got, commentMiddle) {
		t.Fatalf("duolog's comment stayed:\n%s", got)
	}
	if ok, err := folder(f.worktree); err != nil || ok {
		t.Fatalf("worktree still trusted: %v %v", ok, err)
	}
}

// A withdrawal keeps CRLF line ends and leaves the file as it was before.
func TestAdoptWorktreeWithdrawsFromACRLFConfig(t *testing.T) {
	f := newAdoptFixture(t)
	crlf := strings.ReplaceAll(string(f.read(t)), "\n", "\r\n")
	writeFile(t, f.config, crlf)
	if lines, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil || len(lines) == 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	writeFile(t, filepath.Join(f.worktree, ".codex", "rules", "x.rules"), "allow\n")
	if _, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); !errors.Is(err, agent.ErrWorktreeChanged) {
		t.Fatalf("err = %v", err)
	}
	if got := string(f.read(t)); got != crlf {
		t.Fatalf("config =\n%q\nwant\n%q", got, crlf)
	}
}

// Two adoptions at once, in two worktrees, both land.
func TestAdoptWorktreeConcurrentAdoptionsBothLand(t *testing.T) {
	f := newAdoptFixture(t)
	other := filepath.Join(f.lead, ".worktrees", "s5-w1")
	git(t, f.lead, "worktree", "add", "-q", "--detach", other)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, dir := range []string{f.worktree, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Trust{}.AdoptWorktree(f.lead, dir, os.Environ())
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("adoption %d: %v", i, err)
		}
	}
	for _, dir := range []string{f.worktree, other} {
		if ok, err := folder(dir); err != nil || !ok {
			t.Fatalf("%s not trusted: %v %v\n%s", dir, ok, err, f.read(t))
		}
	}
}

// A config that Codex changes while duolog writes is read and planned again.
func TestAdoptWorktreePlansAgainOnAChangedConfig(t *testing.T) {
	f := newAdoptFixture(t)
	calls := 0
	beforeReplace = func() {
		calls++
		if calls == 1 {
			writeFile(t, f.config, string(f.read(t))+"\n[projects.\"/codex-wrote-this\"]\ntrust_level = \"trusted\"\n")
		}
	}
	t.Cleanup(func() { beforeReplace = func() {} })
	lines, err := Trust{}.AdoptWorktree(f.lead, f.worktree, os.Environ())
	if err != nil || len(lines) == 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	if calls != 2 {
		t.Fatalf("replaced after %d plans", calls)
	}
	if !strings.Contains(string(f.read(t)), "/codex-wrote-this") {
		t.Fatal("Codex's change lost")
	}
	if ok, err := folder(f.worktree); err != nil || !ok {
		t.Fatalf("worktree not trusted: %v %v", ok, err)
	}
}

func TestAdoptWorktreeReplacesAnEmptyBackup(t *testing.T) {
	f := newAdoptFixture(t)
	before := f.read(t)
	writeFile(t, f.config+configBackupSuffix, "")
	if _, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil {
		t.Fatal(err)
	}
	if backup, err := os.ReadFile(f.config + configBackupSuffix); err != nil || !bytes.Equal(backup, before) {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}

func TestKeepBackupKeepsAGoodOneAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml"+configBackupSuffix)
	if err := keepBackup(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := keepBackup(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "first" {
		t.Fatalf("backup = %q", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("files left: %v", entries)
	}
	// A folder that cannot take the backup leaves no partial file.
	missing := filepath.Join(dir, "missing", "config.toml"+configBackupSuffix)
	if err := keepBackup(missing, []byte("x"), 0o600); err == nil {
		t.Fatal("backup into a missing folder succeeded")
	}
}

func TestAdoptWorktreeKeepsCRLF(t *testing.T) {
	f := newAdoptFixture(t)
	crlf := strings.ReplaceAll(string(f.read(t)), "\n", "\r\n")
	writeFile(t, f.config, crlf)
	if lines, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil || len(lines) == 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	after := string(f.read(t))
	if !strings.HasPrefix(after, crlf) || strings.Count(after, "\n") != strings.Count(after, "\r\n") {
		t.Fatalf("line endings mixed:\n%q", after)
	}
	if ok, err := folder(f.worktree); err != nil || !ok {
		t.Fatalf("worktree not trusted: %v %v", ok, err)
	}
}

func TestParseWorktreesSplitsOnNUL(t *testing.T) {
	out := "worktree /repo\x00HEAD abc\x00branch refs/heads/main\x00\x00" +
		"worktree /repo/.worktrees/a\nworktree /victim\x00HEAD abc\x00detached\x00\x00" +
		"worktree /gone\x00HEAD abc\x00prunable gitdir file points to non-existent location\x00\x00" +
		"worktree /bare\x00bare\x00\x00"
	got := parseWorktrees([]byte(out))
	want := []worktreeEntry{
		{path: filepath.FromSlash("/repo")},
		{path: filepath.FromSlash("/repo/.worktrees/a\nworktree /victim")},
		{path: filepath.FromSlash("/gone"), prunable: true},
		{path: filepath.FromSlash("/bare"), bare: true},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %+v", got)
	}
	for _, dir := range []string{"/victim", "/gone", "/bare", "/repo"} {
		if listedLinked(got, filepath.FromSlash(dir)) {
			t.Errorf("%s counted as a linked worktree", dir)
		}
	}
}

func TestQuoteRoundTrips(t *testing.T) {
	for _, s := range []string{`C:\Users\a b\repo`, `/a/"quoted"/b`, "/plain"} {
		var m map[string]any
		if err := toml.Unmarshal([]byte("[x."+quote(s)+"]\nk = 1\n"), &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["x"].(map[string]any)[s]; !ok {
			t.Fatalf("%q read back as %v", s, m)
		}
	}
}
