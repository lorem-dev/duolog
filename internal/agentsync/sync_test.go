package agentsync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
)

func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	list := fixtureList(t)
	CatalogList = func(context.Context) ([]models.Entry, string, error) { return list, "codex-test", nil }
	t.Cleanup(func() { CatalogList = nil })
	return root
}

func syncCfg(t *testing.T) config.Sync {
	return config.Sync{Auto: true, Agents: testOpts(t).Config}
}

func put(t *testing.T, root, rel, data string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func agentMD(name, model string) string {
	return "---\nname: " + name + "\ndescription: Does a job.\nmodel: " + model + "\n---\nDo the job.\n"
}

func lockOf(t *testing.T, root string) Lock {
	t.Helper()
	l, err := LoadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestFirstRunNeedsFrom(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{}); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("got %v", err)
	}
	if exists(root, ".codex") || exists(root, ".duolog/sync.lock.json") {
		t.Fatal("wrote something")
	}
}

func TestLaterRunUsesLockDirection(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	if got := lockOf(t, root).Direction; got != "claude" {
		t.Fatal(got)
	}
	put(t, root, ".claude/agents/a.md", agentMD("a", "sonnet"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root, ".codex/agents/a.toml"); !strings.Contains(got, "gpt-6.1-terra") {
		t.Fatalf("got %s", got)
	}
}

func TestOppositeDirectionRefused(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, syncCfg(t), Options{From: "codex"}); err == nil || !strings.Contains(err.Error(), "--switch-direction") {
		t.Fatalf("got %v", err)
	}
	if lockOf(t, root).Direction != "claude" {
		t.Fatal("lock changed")
	}
	if _, err := Run(root, syncCfg(t), Options{From: "codex", SwitchDirection: true}); err != nil {
		t.Fatal(err)
	}
	if lockOf(t, root).Direction != "codex" {
		t.Fatal("not switched")
	}
}

func TestSwitchDirectionRebuildsOldSources(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".codex/agents/a.toml", strings.Replace(read(t, root, ".codex/agents/a.toml"), "Do the job.", "Do it better.", 1), 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "codex", SwitchDirection: true})
	if err != nil || len(rep.Conflicts) != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	got := read(t, root, ".claude/agents/a.md")
	if !strings.Contains(got, "Do it better.") || !strings.Contains(got, "model: opus") {
		t.Fatalf("got %s", got)
	}
	l := lockOf(t, root)
	if e, ok := l.Files[".claude/agents/a.md"]; !ok || e.Source != ".codex/agents/a.toml" {
		t.Fatalf("%+v", l)
	}
	if _, ok := l.Files[".codex/agents/a.toml"]; ok {
		t.Fatal("old target still owned")
	}
}

func TestHandWrittenTargetNotOverwritten(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	put(t, root, ".codex/agents/a.toml", "name = \"a\"\ndescription = \"mine\"\ndeveloper_instructions = \"x\"\n", 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "claude", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 || !strings.Contains(rep.Conflicts[0], ".codex/agents/a.toml") {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(read(t, root, ".codex/agents/a.toml"), "mine") {
		t.Fatal("overwritten")
	}
	if _, ok := lockOf(t, root).Files[".codex/agents/a.toml"]; ok {
		t.Fatal("claimed a hand-written file")
	}
}

func TestHandWrittenStaleFileNotDeleted(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	put(t, root, ".codex/agents/old.toml", "name = \"old\"\n", 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	if !exists(root, ".codex/agents/old.toml") {
		t.Fatal("deleted")
	}
}

func TestOwnedStaleFileDeleted(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(root, ".claude/agents/a.md"))
	rep, err := Run(root, syncCfg(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if exists(root, ".codex/agents/a.toml") || len(rep.Removed) != 1 || len(lockOf(t, root).Files) != 0 {
		t.Fatalf("%+v %+v", rep, lockOf(t, root))
	}
}

func TestEditedGeneratedRefusedWithoutForce(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	edited := read(t, root, ".codex/agents/a.toml") + "# hand edit\n"
	put(t, root, ".codex/agents/a.toml", edited, 0o644)
	put(t, root, ".claude/agents/a.md", agentMD("a", "sonnet"), 0o644)
	rep, err := Run(root, syncCfg(t), Options{})
	if err != nil || len(rep.Conflicts) != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	if read(t, root, ".codex/agents/a.toml") != edited {
		t.Fatal("overwritten")
	}
	rep, err = Run(root, syncCfg(t), Options{Force: true})
	if err != nil || len(rep.Conflicts) != 0 || len(rep.Written) != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	if strings.Contains(read(t, root, ".codex/agents/a.toml"), "hand edit") {
		t.Fatal("force did not overwrite")
	}
}

func TestCheckWritesNothingAndExitsOne(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "claude", Check: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Written) != 1 {
		t.Fatalf("drift not reported: %+v", rep)
	}
	if exists(root, ".codex") || exists(root, ".duolog/sync.lock.json") {
		t.Fatal("check wrote")
	}
}

func TestCheckDetectsUnparsableTarget(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".codex/agents/a.toml", "name = [\n", 0o644)
	rep, err := Run(root, syncCfg(t), Options{Check: true})
	if err != nil || len(rep.Conflicts) != 1 || !strings.Contains(rep.Conflicts[0], "does not parse") {
		t.Fatalf("%v %+v", err, rep)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	root := skillProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "claude", DryRun: true})
	if err != nil || len(rep.Written) != 4 {
		t.Fatalf("%v %+v", err, rep)
	}
	if exists(root, ".codex") || exists(root, ".agents") || exists(root, ".duolog") {
		t.Fatal("dry run wrote")
	}
}

func TestSecondRunIsNoOp(t *testing.T) {
	root := skillProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	files := []string{".codex/agents/a.toml", ".agents/skills/probe/SKILL.md", ".agents/skills/probe/scripts/a.sh", ".duolog/sync.lock.json"}
	old := time.Now().Add(-time.Hour)
	for _, f := range files {
		if err := os.Chtimes(filepath.Join(root, f), old, old); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Run(root, syncCfg(t), Options{})
	if err != nil || len(rep.Written)+len(rep.Removed)+len(rep.Conflicts) != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	for _, f := range files {
		info, _ := os.Stat(filepath.Join(root, f))
		if !info.ModTime().Equal(old.Truncate(time.Nanosecond)) && info.ModTime().After(old.Add(time.Second)) {
			t.Fatalf("%s rewritten", f)
		}
	}
}

func TestPartialFailureLeavesLockConsistent(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/good.md", agentMD("good", "opus"), 0o644)
	put(t, root, ".claude/agents/bad.md", agentMD("bad", "no-such-model"), 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 || !strings.Contains(rep.Conflicts[0], "bad.md") {
		t.Fatalf("%+v", rep)
	}
	if !exists(root, ".codex/agents/good.toml") || exists(root, ".codex/agents/bad.toml") {
		t.Fatal("wrong files")
	}
	l := lockOf(t, root)
	if len(l.Files) != 1 {
		t.Fatalf("%+v", l)
	}
	e := l.Files[".codex/agents/good.toml"]
	if e.TargetHash != hashBytes([]byte(read(t, root, ".codex/agents/good.toml"))) || e.ResolvedModel != "gpt-6.1-sol" || e.Source != ".claude/agents/good.md" {
		t.Fatalf("%+v", e)
	}
}

func TestLockKeysSorted(t *testing.T) {
	root := newProject(t)
	for _, n := range []string{"c", "a", "b"} {
		put(t, root, ".claude/agents/"+n+".md", agentMD(n, "opus"), 0o644)
	}
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	data := read(t, root, ".duolog/sync.lock.json")
	if !json.Valid([]byte(data)) || strings.Index(data, "a.toml") > strings.Index(data, "b.toml") || strings.Index(data, "b.toml") > strings.Index(data, "c.toml") {
		t.Fatal(data)
	}
}

func TestHookRebuildsOnlyTouchedSkill(t *testing.T) {
	root := skillProject(t)
	put(t, root, ".claude/skills/other/SKILL.md", "---\nname: other\ndescription: d\n---\nv1\n", 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".claude/skills/probe/references/b.md", "# B2\n", 0o644)
	put(t, root, ".claude/skills/other/SKILL.md", "---\nname: other\ndescription: d\n---\nv2\n", 0o644)
	if line := Hook(root, syncCfg(t), filepath.Join(root, ".claude/skills/probe/references/b.md")); line != "" {
		t.Fatalf("got %q", line)
	}
	if read(t, root, ".agents/skills/probe/references/b.md") != "# B2\n" {
		t.Fatal("touched skill not rebuilt")
	}
	if strings.Contains(read(t, root, ".agents/skills/other/SKILL.md"), "v2") {
		t.Fatal("untouched skill rebuilt")
	}
}

func TestHookSilentForUnrelatedFile(t *testing.T) {
	root := skillProject(t)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, "main.go", "package main\n", 0o644)
	if line := Hook(root, syncCfg(t), filepath.Join(root, "main.go")); line != "" {
		t.Fatalf("got %q", line)
	}
	if line := Hook(root, syncCfg(t), "/elsewhere/x.md"); line != "" {
		t.Fatalf("got %q", line)
	}
}

func TestHookSilentWithoutDirection(t *testing.T) {
	root := skillProject(t)
	if line := Hook(root, syncCfg(t), ".claude/skills/probe/SKILL.md"); line != "" {
		t.Fatalf("got %q", line)
	}
	if line := SessionStart(root, syncCfg(t)); line != "" {
		t.Fatalf("got %q", line)
	}
	if exists(root, ".agents") || exists(root, ".duolog") {
		t.Fatal("wrote")
	}
}

func TestHookSilentWhenAutoOff(t *testing.T) {
	root := skillProject(t)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".claude/skills/probe/references/b.md", "# B2\n", 0o644)
	cfg := syncCfg(t)
	cfg.Auto = false
	if Hook(root, cfg, ".claude/skills/probe/references/b.md") != "" || SessionStart(root, cfg) != "" {
		t.Fatal("not silent")
	}
	if read(t, root, ".agents/skills/probe/references/b.md") != "# B\n" {
		t.Fatal("rebuilt")
	}
}

func TestHookNamesSourceWhenTargetEdited(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	edited := read(t, root, ".codex/agents/a.toml") + "# edit\n"
	put(t, root, ".codex/agents/a.toml", edited, 0o644)
	line := Hook(root, syncCfg(t), filepath.Join(root, ".codex/agents/a.toml"))
	if !strings.Contains(line, ".claude/agents/a.md") || strings.Contains(line, "\n") || line == "" {
		t.Fatalf("got %q", line)
	}
	if read(t, root, ".codex/agents/a.toml") != edited {
		t.Fatal("target changed")
	}
}

func TestHookReportsRefusalInOneLine(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".claude/agents/a.md", agentMD("a", "no-such-model"), 0o644)
	line := Hook(root, syncCfg(t), ".claude/agents/a.md")
	if !strings.HasPrefix(line, "duolog sync: ") || !strings.Contains(line, "a.md") || strings.Contains(line, "\n") {
		t.Fatalf("got %q", line)
	}
	if !strings.Contains(read(t, root, ".codex/agents/a.toml"), "gpt-6.1-sol") {
		t.Fatal("target changed")
	}
}

func withNewSol(t *testing.T) {
	list := append(fixtureList(t), models.Entry{Slug: "gpt-6.2-sol", Visibility: "list"})
	CatalogList = func(context.Context) ([]models.Entry, string, error) { return list, "codex-test-2", nil }
}

func TestNewModelVersionRebuildsAgents(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	if lockOf(t, root).Files[".codex/agents/a.toml"].ResolvedModel != "gpt-6.1-sol" {
		t.Fatal("not recorded")
	}
	withNewSol(t)
	if line := SessionStart(root, syncCfg(t)); line != "" {
		t.Fatalf("got %q", line)
	}
	if !strings.Contains(read(t, root, ".codex/agents/a.toml"), "gpt-6.2-sol") {
		t.Fatal("agent not rewritten")
	}
	if lockOf(t, root).Files[".codex/agents/a.toml"].ResolvedModel != "gpt-6.2-sol" {
		t.Fatal("lock not updated")
	}
}

func TestSameModelVersionWritesNothing(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	for _, f := range []string{".codex/agents/a.toml", ".duolog/sync.lock.json"} {
		os.Chtimes(filepath.Join(root, f), old, old)
	}
	if line := SessionStart(root, syncCfg(t)); line != "" {
		t.Fatalf("got %q", line)
	}
	for _, f := range []string{".codex/agents/a.toml", ".duolog/sync.lock.json"} {
		info, _ := os.Stat(filepath.Join(root, f))
		if info.ModTime().After(old.Add(time.Second)) {
			t.Fatalf("%s rewritten", f)
		}
	}
}

func TestSessionStartCatchesShellEdits(t *testing.T) {
	root := skillProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".claude/agents/a.md", agentMD("a", "sonnet"), 0o644)
	put(t, root, ".claude/skills/probe/references/b.md", "# B3\n", 0o644)
	if line := SessionStart(root, syncCfg(t)); line != "" {
		t.Fatalf("got %q", line)
	}
	if !strings.Contains(read(t, root, ".codex/agents/a.toml"), "gpt-6.1-terra") || read(t, root, ".agents/skills/probe/references/b.md") != "# B3\n" {
		t.Fatal("not rebuilt")
	}
}

func TestAgentNameWithPathIsRefused(t *testing.T) {
	for _, name := range []string{"../../x", "a/b", ".hidden", `a\b`} {
		root := newProject(t)
		put(t, root, ".claude/agents/a.md", agentMD(name, "opus"), 0o644)
		rep, err := Run(root, syncCfg(t), Options{From: "claude"})
		if err != nil || len(rep.Conflicts) != 1 || !strings.Contains(rep.Conflicts[0], "plain file name") {
			t.Fatalf("%q: %v %+v", name, err, rep)
		}
		if len(rep.Written) != 0 || exists(root, ".codex") || exists(filepath.Dir(root), "x.toml") {
			t.Fatalf("%q: wrote %+v", name, rep.Written)
		}
		if len(lockOf(t, root).Files) != 0 {
			t.Fatalf("%q: lock owns files", name)
		}
	}
}

func TestApplyRefusesTargetOutsideGeneratedFolder(t *testing.T) {
	root := newProject(t)
	r := &runner{root: root, to: sideDirs("codex")}
	if r.within(filepath.Join(root, ".codex", "agents", "..", "..", "x.toml")) || !r.within(filepath.Join(root, ".codex", "agents", "a.toml")) {
		t.Fatal("within is wrong")
	}
}

func TestSwitchDirectionKeepsEditedOldSource(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0o644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	edited := agentMD("a", "opus") + "My own edit.\n"
	put(t, root, ".claude/agents/a.md", edited, 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "codex", SwitchDirection: true})
	if err != nil {
		t.Fatal(err)
	}
	if read(t, root, ".claude/agents/a.md") != edited {
		t.Fatal("edit lost")
	}
	if len(rep.Conflicts) != 1 || !strings.Contains(rep.Conflicts[0], "written by hand") {
		t.Fatalf("%+v", rep)
	}
}
