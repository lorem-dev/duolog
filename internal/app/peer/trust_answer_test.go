package peer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// claudePane is Claude's real pane support, through panetest: the trust tests
// check what Claude itself reads from its config.
func claudePane() PaneAgent { return testPane("claude").Agent }

// trustRig is a lead repository with a linked worktree, a second repository
// and a Claude user state under a home folder of its own.
type trustRig struct {
	t                         *testing.T
	home, lead, linked, other string
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo makes dir a repository with one commit.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "first")
}

func newTrustRig(t *testing.T) *trustRig {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &trustRig{t: t, home: filepath.Join(base, "home"), lead: filepath.Join(base, "lead"), linked: filepath.Join(base, "linked"), other: filepath.Join(base, "other")}
	if err := os.MkdirAll(r.home, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, r.lead)
	initRepo(t, r.other)
	gitIn(t, r.lead, "worktree", "add", "-q", "-b", "side", r.linked)
	return r
}

func writeClaudeState(t *testing.T, home string, projects map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *trustRig) trust(root string) {
	r.t.Helper()
	writeClaudeState(r.t, r.home, map[string]any{root: map[string]any{"hasTrustDialogAccepted": true}})
}

func (r *trustRig) allowed(folder string) bool {
	return trustAllowed(claudePane(), r.lead, folder, r.environ())
}

// environ names the rig's home as Claude reads it on every system: HOME, and
// USERPROFILE on Windows.
func (r *trustRig) environ() []string {
	return []string{"HOME=" + r.home, "USERPROFILE=" + r.home}
}

func TestTrustAllowedForTheLeadRootASubfolderAndALinkedWorktree(t *testing.T) {
	r := newTrustRig(t)
	r.trust(r.lead)
	sub := filepath.Join(r.lead, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, folder := range map[string]string{"root": r.lead, "subfolder": sub, "linked worktree": r.linked} {
		if !r.allowed(folder) {
			t.Errorf("%s: trust refused", name)
		}
	}
}

func TestTrustRefusedForAnotherRepository(t *testing.T) {
	r := newTrustRig(t)
	r.trust(r.lead)
	if r.allowed(r.other) {
		t.Fatal("trust given to another repository")
	}
	plain := filepath.Join(filepath.Dir(r.lead), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if r.allowed(plain) || r.allowed("") {
		t.Fatal("trust given to a folder outside any repository of the lead")
	}
}

// A folder's own .git file is its claim, not the lead's. Only a worktree that
// the lead's repository lists is one.
func TestTrustRefusedForAFolderThatClaimsToBeAWorktree(t *testing.T) {
	r := newTrustRig(t)
	r.trust(r.lead)
	linkedName := filepath.Base(r.linked)
	for name, gitdir := range map[string]string{
		"lead's .git":        filepath.Join(r.lead, ".git"),
		"lead's worktree":    filepath.Join(r.lead, ".git", "worktrees", linkedName),
		"a missing worktree": filepath.Join(r.lead, ".git", "worktrees", "nothing"),
	} {
		planted := filepath.Join(filepath.Dir(r.lead), "planted-"+strings.NewReplacer(" ", "-", "'", "").Replace(name))
		if err := os.MkdirAll(planted, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(planted, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if r.allowed(planted) {
			t.Errorf("%s: trust given to a folder that only claims it", name)
		}
	}
}

// A worktree path may hold a newline. Listed raw, it could add any folder to
// the list, so the list is read NUL-separated.
func TestTrustRefusedForAFolderNamedInsideAWorktreePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows allows no newline in a file name")
	}
	r := newTrustRig(t)
	r.trust(r.lead)
	evil := r.linked + "-x\nworktree " + r.other
	gitIn(t, r.lead, "worktree", "add", "-q", "-b", "evil", evil)
	if r.allowed(r.other) {
		t.Fatal("a folder injected through a worktree path was trusted")
	}
	if !r.allowed(r.linked) {
		t.Fatal("the real worktree was refused")
	}
}

// A listed worktree whose folder was deleted is stale: another repository at
// its path is not the lead's.
func TestTrustRefusedForAStaleWorktreePath(t *testing.T) {
	r := newTrustRig(t)
	r.trust(r.lead)
	if err := os.RemoveAll(r.linked); err != nil {
		t.Fatal(err)
	}
	initRepo(t, r.linked)
	if r.allowed(r.linked) {
		t.Fatal("another repository at a stale worktree path was trusted")
	}
}

// A git variable in duolog's own environment moves every git question to one
// repository; the answer must not follow it.
func TestTrustIgnoresGitEnvironmentVariables(t *testing.T) {
	r := newTrustRig(t)
	r.trust(r.lead)
	plain := filepath.Join(filepath.Dir(r.lead), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(r.lead, ".git"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(r.lead, ".git"))
	t.Setenv("GIT_WORK_TREE", r.lead)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(r.lead))
	t.Setenv("GIT_DISCOVERY_ACROSS_FILESYSTEM", "1")
	if r.allowed(r.other) || r.allowed(plain) {
		t.Fatal("trust given to another folder through GIT_DIR")
	}
	if !r.allowed(r.linked) {
		t.Fatal("the lead's own worktree was refused under GIT_DIR")
	}
}

func TestTrustRefusedWhenTheLeadRootIsNotTrusted(t *testing.T) {
	r := newTrustRig(t)
	if r.allowed(r.lead) {
		t.Fatal("trust given with no state")
	}
	writeClaudeState(t, r.home, map[string]any{r.lead: map[string]any{"hasTrustDialogAccepted": false}})
	if r.allowed(r.lead) || r.allowed(r.linked) {
		t.Fatal("trust given for a root whose dialog was not accepted")
	}
	// Claude counts a trusted parent of the lead root too.
	r.trust(filepath.Dir(r.lead))
	if !r.allowed(r.lead) {
		t.Fatal("a root below a trusted parent was refused")
	}
}

func TestTrustRefusedWhenTheConfigCannotBeRead(t *testing.T) {
	r := newTrustRig(t)
	if err := os.WriteFile(filepath.Join(r.home, ".claude.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r.allowed(r.lead) {
		t.Fatal("trust given from an unreadable config")
	}
	if trustAllowed(claudePane(), r.lead, r.lead, []string{}) {
		t.Fatal("trust given with no home")
	}
}

func TestCodexPaneDoesNotReadClaudesTrustQuestion(t *testing.T) {
	if _, _, ok := testPane("codex").Agent.FolderTrust(parseFor("claude", []byte(fixtureText("claude-trust-folder.txt")))); ok {
		t.Fatal("Codex read Claude's trust question")
	}
	if _, _, ok := claudePane().FolderTrust(parseFor("codex", []byte(fixtureText("codex-trust-live.txt")))); ok {
		t.Fatal("Claude read Codex's trust question")
	}
}

func TestNamedFolderMustBeThePaneFolder(t *testing.T) {
	r := newTrustRig(t)
	sub := filepath.Join(r.lead, "pkg")
	if !namedFoldersOK([]string{r.linked}, r.lead, r.linked) || !namedFoldersOK([]string{sub, r.linked}, r.lead, r.linked) {
		t.Fatal("a screen that names only the lead's folders, the pane folder among them, was refused")
	}
	for name, named := range map[string][]string{
		"another repository":    {r.other},
		"nothing":               nil,
		"not the pane folder":   {r.lead},
		"an extra foreign path": {"/elsewhere", r.linked},
		"a foreign repository":  {r.linked, r.other},
	} {
		if namedFoldersOK(named, r.lead, r.linked) {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A lead root inside an outer repository has no worktree list of its own: the
// outer repository, its root and its worktrees are not the lead's project.
func TestTrustRefusedForTheOuterRepositoryOfTheLeadRoot(t *testing.T) {
	r := newTrustRig(t)
	lead := filepath.Join(r.lead, "projects", "lead")
	if err := os.MkdirAll(lead, 0o755); err != nil {
		t.Fatal(err)
	}
	r.trust(lead)
	environ := r.environ()
	if !trustAllowed(claudePane(), lead, lead, environ) {
		t.Fatal("the lead root itself was refused")
	}
	if trustAllowed(claudePane(), lead, r.lead, environ) || trustAllowed(claudePane(), lead, r.linked, environ) {
		t.Fatal("the outer repository or its worktree was trusted")
	}
}

// openWorktreeRig is a rig whose state root is a trusted repository and whose
// pane opens in a linked worktree that Claude has never seen, with a
// .mcp.json of the servers a and b, of which the lead enabled a.
func openWorktreeRig(t *testing.T, name string) (*rig, string) {
	t.Helper()
	r := newRig(t, withScreens(claudeIdle("")))
	initRepo(t, r.root)
	wt := filepath.Join(evalDir(t, t.TempDir()), name)
	gitIn(t, r.root, "worktree", "add", "-q", "-b", name, wt)
	if err := os.WriteFile(filepath.Join(wt, ".mcp.json"), []byte(`{"mcpServers":{"a":{"command":"x"},"b":{"command":"y"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeClaudeState(t, os.Getenv("HOME"), map[string]any{r.root: map[string]any{
		"hasTrustDialogAccepted": true, "enabledMcpjsonServers": []string{"a"},
	}})
	// A plan pane opens only where the gate runs, in the worktree it opens in.
	writeFile(t, filepath.Join(wt, ".claude", "settings.local.json"), `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
	t.Chdir(wt)
	return r, wt
}

func evalDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func trustScreens(t *testing.T, folder string) (ask, yes string) {
	t.Helper()
	ask = strings.Replace(fixtureText("claude-trust-folder.txt"), "/tmp/probe/t/ci", folder, 1)
	ask = strings.Replace(ask, "/tmp/<scratchpad>", folder, 1)
	yes = strings.Replace(strings.Replace(ask, "❯ No, exit", "  No, exit", 1), "  Yes, I trust this folder", "❯ Yes, I trust this folder", 1)
	return ask, yes
}

// A new linked worktree is a folder Claude has not seen, so it asks. The lead's
// own root is trusted, so duolog answers; the MCP choices are the lead root's.
func TestClaudeFolderTrustQuestionIsAnsweredInAnUntrustedLinkedWorktree(t *testing.T) {
	r, wt := openWorktreeRig(t, "wt")
	ask, yes := trustScreens(t, wt)
	r.panes(withScreens(ask, yes, claudeIdle(""), claudeIdle("")))
	e := r.open("plan")
	if e.State != Ready {
		t.Fatalf("%+v", e)
	}
	if got := strings.Join(r.keys(), ","); got != "down,enter" {
		t.Fatalf("keys %s", got)
	}
	if len(r.messages("ask")) != 0 || r.entry(e.ID).Asked != "" {
		t.Fatal("the trust question reached the lead")
	}
	log, err := os.ReadFile(filepath.Join(r.root, ".duolog", "decisions.ndjson"))
	if err != nil || !strings.Contains(string(log), "Yes, I trust this folder") {
		t.Fatalf("%s %v", log, err)
	}
	launched := r.started()
	argv := launched[slices.Index(launched, "claude"):]
	if !strings.Contains(argv[2], `"enabledMcpjsonServers":["a"]`) || !strings.Contains(argv[2], `"disabledMcpjsonServers":["b"]`) {
		t.Fatalf("the lead root's MCP choices did not reach the pane: %s", argv[2])
	}
}

func TestClaudeFolderTrustQuestionStaysWhenTheScreenNamesAnotherFolder(t *testing.T) {
	r, _ := openWorktreeRig(t, "wt")
	other := evalDir(t, t.TempDir())
	ask, _ := trustScreens(t, other)
	r.panes(withScreens(ask))
	e := r.open("plan")
	if e.State == Ready || len(r.keys()) != 0 {
		t.Fatalf("%+v keys %v", e, r.keys())
	}
}

func TestClaudeFolderTrustQuestionStaysForTheLeadWhenTheRootIsNotTrusted(t *testing.T) {
	r, wt := openWorktreeRig(t, "wt")
	writeClaudeState(t, os.Getenv("HOME"), map[string]any{})
	ask, _ := trustScreens(t, wt)
	r.panes(withScreens(ask))
	e := r.open("plan")
	if e.State == Ready || len(r.keys()) != 0 {
		t.Fatalf("%+v keys %v", e, r.keys())
	}
}

func TestClaudeFolderTrustQuestionStaysWhenAnotherNamedFolderIsForeign(t *testing.T) {
	r, wt := openWorktreeRig(t, "wt")
	ask, _ := trustScreens(t, wt)
	ask = strings.Replace(ask, wt, evalDir(t, t.TempDir()), 1)
	r.panes(withScreens(ask))
	e := r.open("plan")
	if e.State == Ready || len(r.keys()) != 0 {
		t.Fatalf("%+v keys %v", e, r.keys())
	}
}

// The default capture keeps the last 20 non-blank lines. A folder that
// pre-approves many tools pushes the "Accessing workspace" lines off it, so the
// question is read again from a longer capture.
func TestClaudeFolderTrustQuestionIsAnsweredWhenAWarningBlockPushesTheHeadingOff(t *testing.T) {
	r, wt := openWorktreeRig(t, "wt")
	ask, yes := trustScreens(t, wt)
	var many strings.Builder
	for i := range 14 {
		many.WriteString("   Bash(tool" + string(rune('a'+i)) + ":*)\n")
	}
	long := func(s string) string {
		return strings.Replace(s, "   Bash(echo:*)\n", "   Bash(echo:*)\n"+many.String(), 1)
	}
	r.panes(withScreens(long(ask), long(ask), long(yes), claudeIdle(""), claudeIdle("")))
	e := r.open("plan")
	if e.State != Ready || strings.Join(r.keys(), ",") != "down,enter" {
		t.Fatalf("%+v keys %v", e, r.keys())
	}
}

// Output that quotes the trust screen's words must not stop the gate or a rule.
// The screens repeat, as a live pane's re-capture does.
func TestTrustWordsInEarlierOutputDoNotBlockTheGateOrARule(t *testing.T) {
	quote := "• Read trust.go\nAccessing workspace: is what the dialog says\nQuick safety check\n"
	t.Run("command prompt", func(t *testing.T) {
		prompt := quote + commandPrompt("CHECK=1 go test ./...", false)
		r := newRig(t, withScreens(prompt, prompt, codexIdle(""), codexIdle("")))
		r.cfg.Policy.Rules = append(r.cfg.Policy.Rules, config.Rule{Action: "exec", Match: "go test *", Decision: "allow", Roles: []string{"implement"}})
		e := r.open("implement")
		// The repeated screen is answered once per poll.
		if keys := r.keys(); e.State != Ready || len(keys) == 0 || keys[0] != "enter" || len(r.messages("ask")) != 0 {
			t.Fatalf("%+v keys %v", e, keys)
		}
	})
	t.Run("rule", func(t *testing.T) {
		dialog := quote + strings.Repeat("output line\n", 40) + "Which environment should I use?\n› 1. Staging\n  2. Production\n"
		r := newRig(t, withScreens(dialog, dialog, codexIdle(""), codexIdle("")))
		r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "Which environment", Answer: "Staging"}}
		e := r.open("implement")
		if keys := r.keys(); e.State != Ready || len(keys) == 0 || keys[0] != "enter" || len(r.messages("ask")) != 0 {
			t.Fatalf("%+v keys %v", e, keys)
		}
	})
}

// A command prompt left in the scrollback above a trust screen is not the
// question: the gate must not read its command and answer the trust screen,
// even when the trust screen's first option is the gate's "allow" label.
func TestStaleCommandPromptAboveATrustScreenSendsNoKeys(t *testing.T) {
	stale := commandPrompt("CHECK=1 go test ./...", false)
	trust := "Folder access\n  /tmp/some/folder\n\nTrust this folder?\n\n› 1. Yes, proceed\n  2. Quit\n"
	screen := stale + "\n• Ran go test\n  ok\n\n" + trust
	r := newRig(t, withScreens(screen, screen))
	r.cfg.Policy.Rules = append(r.cfg.Policy.Rules, config.Rule{Action: "exec", Match: "go test *", Decision: "allow", Roles: []string{"implement"}})
	e := r.open("implement")
	if len(r.keys()) != 0 || e.State == Ready {
		t.Fatalf("%+v keys %v", e, r.keys())
	}
}

// When the longer capture no longer shows the trust screen, the old screen is
// not acted on.
func TestTrustRecaptureThatMovedOnSendsNothing(t *testing.T) {
	r, wt := openWorktreeRig(t, "wt")
	ask, _ := trustScreens(t, wt)
	var many strings.Builder
	for i := range 14 {
		many.WriteString("   Bash(tool" + string(rune('a'+i)) + ":*)\n")
	}
	long := strings.Replace(ask, "   Bash(echo:*)\n", "   Bash(echo:*)\n"+many.String(), 1)
	r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "Security guide", Answer: "No, exit"}}
	r.panes(withScreens(long, claudeIdle(""), claudeIdle("")))
	r.open("plan")
	if len(r.keys()) != 0 {
		t.Fatalf("keys %v", r.keys())
	}
}

// A project rule cannot answer a trust screen, whether or not the built-in
// answer recognises the screen.
func TestAutoAnswerRuleNeverAnswersATrustScreen(t *testing.T) {
	for name, c := range map[string]struct {
		edit   func(string) string
		answer string
		// trusted keeps the lead root trusted, so only the handled path, not a
		// refusal of the folder, keeps the rule out.
		trusted bool
	}{
		"plain": {func(s string) string { return s }, "Yes, I trust this folder", false},
		"third option": {func(s string) string {
			return strings.Replace(s, "  Yes, I trust this folder\n", "  Yes, I trust this folder\n   Review settings first\n", 1)
		}, "Yes, I trust this folder", false},
		"no heading": {func(s string) string { return strings.Replace(s, "Accessing workspace:", "Workspace:", 1) }, "Yes, I trust this folder", false},
		// The label is one this version does not know, and the rule's answer
		// passes SafeAutoAnswer. Only the screen's text marks it a trust screen.
		"relabelled, trusted root":   {func(s string) string { return strings.Replace(s, "Yes, I trust this folder", "Yes, proceed", 1) }, "Yes, proceed", true},
		"relabelled, untrusted root": {func(s string) string { return strings.Replace(s, "Yes, I trust this folder", "Yes, proceed", 1) }, "Yes, proceed", false},
	} {
		r, wt := openWorktreeRig(t, "wt")
		if !c.trusted {
			writeClaudeState(t, os.Getenv("HOME"), map[string]any{})
		}
		r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "Quick safety check", Answer: c.answer}}
		ask, _ := trustScreens(t, wt)
		r.panes(withScreens(c.edit(ask)))
		e := r.open("plan")
		if e.State == Ready || len(r.keys()) != 0 {
			t.Errorf("%s: %+v keys %v", name, e, r.keys())
		}
	}
}
