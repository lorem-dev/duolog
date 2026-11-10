package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// AdoptWorktree gives dir, a linked worktree of the lead's repository, the
// folder trust and the approvals of duolog's own hooks that Codex holds for
// the lead root or the repository's main checkout. A new linked worktree is a
// new folder to Codex: without this, Codex neither trusts it nor runs
// duolog's hooks there until the person opens /hooks in it.
//
// It writes only when every check passes: git, run without any GIT_
// variable, lists dir as a linked worktree of the repository whose top is
// lead, and dir's common Git folder is lead's; Codex trusts the source root
// and has approved duolog's PreToolUse gate there; and each approval it
// copies is of a hook that duolog install writes, at the same place and the
// same in both hooks files, and not one that install made stale. It changes
// nothing else in config.toml: the new tables go at its end, the result must
// read as the old config plus exactly them, the first change keeps a backup
// and the file is replaced in one rename. It never touches a sandbox or an
// approval policy. The lines say what it wrote; none means nothing changed.
//
// Trust lets Codex load the worktree's own .codex folder, which its branch
// may change, so trust is written only while that folder holds the same as
// the source root's, hooks.json aside, and checked again on every call. Once
// they differ, the tables that duolog wrote for dir, marked by its comment,
// come out again under the same lock and backup rules, and the error wraps
// agent.ErrWorktreeChanged: Codex is not to run there until the person
// trusts the folder. A trust that duolog did not write is never removed.
func (Trust) AdoptWorktree(lead, dir string, environ []string) ([]string, error) {
	if !plainPath(lead) || !plainPath(dir) || !filepath.IsAbs(lead) || !filepath.IsAbs(dir) || samePath(lead, dir) {
		return nil, nil
	}
	lead, dir = filepath.Clean(lead), filepath.Clean(dir)
	codexHome, home, ok := codexHomes(environ)
	if !ok {
		return nil, nil
	}
	a := adopter{
		path:      filepath.Join(codexHome, "config.toml"),
		dir:       dir,
		roots:     []string{lead},
		userHooks: filepath.Join(codexHome, "hooks.json"),
		stale:     install.StaleApprovalsIn(home, environ),
	}
	// A link at the config path is followed: the file it names is the one
	// locked and replaced.
	if target, err := filepath.EvalSymlinks(a.path); err == nil {
		a.path = target
	}
	if lines, done, err := a.recheck(); done {
		return lines, err
	}
	if !needsAdoption(dir, environ) {
		return nil, nil
	}
	main, ok := linkedWorktree(lead, dir)
	if !ok {
		return nil, nil
	}
	if main != "" && !samePath(main, lead) {
		a.roots = append(a.roots, main)
	}
	return a.adopt()
}

// needsAdoption reports whether Codex does not yet both trust dir and run
// duolog's gate there, for the process whose environment is environ.
func needsAdoption(dir string, environ []string) bool {
	folder, err := Trust{}.Folder(dir, environ)
	if err != nil || !folder {
		return true
	}
	hooks, err := Trust{}.Hooks(dir, environ)
	return err != nil || !hooks.Trusted || hooks.Unproven != ""
}

// codexHomes returns the Codex home and the home folder that environ selects.
func codexHomes(environ []string) (codexHome, home string, ok bool) {
	home, homeErr := trusthome.HomeIn(environ)
	codexHome, codexErr := kit.Home(environ, "CODEX_HOME", ".codex")
	return codexHome, home, homeErr == nil && codexErr == nil
}

// plainPath reports whether p can be compared, listed and written as a TOML
// key safely: valid UTF-8 with no control character, such as a newline.
func plainPath(p string) bool {
	return p != "" && utf8.ValidString(p) && !strings.ContainsFunc(p, unicode.IsControl)
}

// samePath reports whether two spellings name one folder.
func samePath(a, b string) bool {
	return pathutil.Clean(pathutil.Resolved(a)) == pathutil.Clean(pathutil.Resolved(b))
}

// gitEnv is the environment of duolog without any GIT_ variable, so that no
// inherited GIT_DIR, GIT_WORK_TREE or GIT_COMMON_DIR points git at another
// repository.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// gitPath runs git in dir with env and reads the one path it prints, made
// absolute against dir. ok is false when git fails or prints anything else.
func gitPath(ctx context.Context, env []string, dir string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	path := strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
	if !plainPath(path) {
		return "", false
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), true
}

// worktreeEntry is one record of git worktree list --porcelain -z.
type worktreeEntry struct {
	path           string
	bare, prunable bool
}

// parseWorktrees reads the records of git worktree list --porcelain -z: each
// attribute ends in NUL and an empty one ends the record, so a path may hold
// any character but NUL.
func parseWorktrees(out []byte) []worktreeEntry {
	var entries []worktreeEntry
	open := false
	for _, field := range strings.Split(string(out), "\x00") {
		switch {
		case field == "":
			open = false
		case strings.HasPrefix(field, "worktree "):
			entries = append(entries, worktreeEntry{path: filepath.FromSlash(strings.TrimPrefix(field, "worktree "))})
			open = true
		case !open:
		case field == "bare":
			entries[len(entries)-1].bare = true
		case field == "prunable" || strings.HasPrefix(field, "prunable "):
			entries[len(entries)-1].prunable = true
		}
	}
	return entries
}

// listedLinked reports whether dir is one of the linked worktrees in entries:
// not the first, which is the main checkout or the bare repository, and
// neither bare nor prunable.
func listedLinked(entries []worktreeEntry, dir string) bool {
	for i, e := range entries {
		if i > 0 && !e.bare && !e.prunable && plainPath(e.path) && filepath.IsAbs(e.path) && samePath(e.path, dir) {
			return true
		}
	}
	return false
}

// linkedWorktree proves that dir is a linked worktree of the repository whose
// top is lead, with git run without any GIT_ variable: lead is its own top,
// git worktree list of lead names dir as a linked worktree, dir is its own
// top, and dir's common Git folder is lead's. main is the repository's main
// checkout, "" for a bare repository.
func linkedWorktree(lead, dir string) (main string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env := gitEnv()
	if top, ok := gitPath(ctx, env, lead, "rev-parse", "--show-toplevel"); !ok || !samePath(top, lead) {
		return "", false
	}
	cmd := exec.CommandContext(ctx, "git", "-C", lead, "worktree", "list", "--porcelain", "-z")
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	entries := parseWorktrees(out)
	if !listedLinked(entries, dir) {
		return "", false
	}
	if top, ok := gitPath(ctx, env, dir, "rev-parse", "--show-toplevel"); !ok || !samePath(top, dir) {
		return "", false
	}
	leadCommon, ok := gitPath(ctx, env, lead, "rev-parse", "--git-common-dir")
	if !ok {
		return "", false
	}
	if common, ok := gitPath(ctx, env, dir, "rev-parse", "--git-common-dir"); !ok || !samePath(common, leadCommon) {
		return "", false
	}
	if first := entries[0]; !first.bare && plainPath(first.path) && filepath.IsAbs(first.path) {
		main = first.path
	}
	return main, true
}

// adoptConfig is what planAdoption reads of config.toml.
type adoptConfig struct {
	Projects map[string]map[string]any `toml:"projects"`
	Hooks    struct {
		State map[string]map[string]any `toml:"state"`
	} `toml:"hooks"`
}

// approvalCopy is one hook approval written for the worktree.
type approvalCopy struct {
	key, event, position, hash string
	entry                      map[string]any
}

// adoption is what AdoptWorktree writes: the worktree's trust, unless Codex
// trusts it already, and the approvals it copies from the source root.
type adoption struct {
	dir, from string
	trustKey  string // "" when Codex trusts dir already
	approvals []approvalCopy
}

// codexEvents maps each spelling of a hook event in an approval key, Codex's
// snake_case and hooks.json's PascalCase, to the PascalCase name, for the
// events duolog installs.
func codexEvents() map[string]string {
	m := map[string]string{}
	for _, e := range events(install.GateTimeout) {
		m[e.Name] = e.Name
		var b strings.Builder
		for i, r := range e.Name {
			if i > 0 && unicode.IsUpper(r) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		}
		m[b.String()] = e.Name
	}
	return m
}

// splitApproval splits a Codex approval key into its hooks file, event and
// position (group:handler).
func splitApproval(key string) (path, event, position string, ok bool) {
	parts := strings.Split(key, ":")
	if len(parts) < 4 {
		return "", "", "", false
	}
	n := len(parts)
	for _, p := range parts[n-2:] {
		if _, err := strconv.Atoi(p); err != nil || p == "" || strings.ContainsAny(p, "+-") {
			return "", "", "", false
		}
	}
	return strings.Join(parts[:n-3], ":"), parts[n-3], parts[n-2] + ":" + parts[n-1], true
}

// hooksDoc is a Codex hooks file, read both as duolog's hook groups and as
// plain JSON values for an exact comparison.
type hooksDoc struct {
	groups map[string][]install.HookGroup
	raw    map[string][]any
}

func readHooksDoc(path string) (hooksDoc, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return hooksDoc{}, false
	}
	var typed struct {
		Hooks map[string][]install.HookGroup `json:"hooks"`
	}
	var raw struct {
		Hooks map[string][]any `json:"hooks"`
	}
	if json.Unmarshal(data, &typed) != nil || json.Unmarshal(data, &raw) != nil {
		return hooksDoc{}, false
	}
	return hooksDoc{groups: typed.Hooks, raw: raw.Hooks}, true
}

// ownHandler reports whether handler j of group g of event is exactly one
// that duolog install writes for Codex, or its dev launcher form in duolog's
// own repository: its command, with no args, run in the foreground, in a
// group that covers every tool. adopt checks that a dev launcher it copies
// trust for is the same in both checkouts.
func ownHandler(event string, g install.HookGroup, j int) bool {
	if j >= len(g.Hooks) || !g.CoversEveryTool() {
		return false
	}
	h := g.Hooks[j]
	if h.Type != "command" || len(h.Args) != 0 || h.Async || h.AsyncRewake {
		return false
	}
	return ownCommands(event, h.Command, h.CommandWindows, true)
}

// devLaunchers are the dev launcher files a dev form hook runs, below the
// checkout root.
var devLaunchers = []string{"scripts/dev/bin/duolog", "scripts/dev/bin/duolog.ps1"}

// sameDevLaunchers reports whether the hooks of root run no dev launcher, or
// root's dev launchers are duolog's and dir has the same bytes: copying the
// person's approval then runs no other program in dir.
func sameDevLaunchers(root, dir string) bool {
	data, err := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
	if err != nil || !strings.Contains(string(data), "scripts/dev/bin/duolog") {
		return true
	}
	if !install.IsDevLauncher(filepath.Join(root, filepath.FromSlash(devLaunchers[0]))) {
		return false
	}
	for _, name := range devLaunchers {
		a, errA := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		b, errB := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			return false
		}
	}
	return true
}

// approvalEntry returns the approval entry when it holds a trusted_hash and
// nothing duolog does not know: an entry the person switched off, or one with
// other keys, is not copied.
func approvalEntry(entry map[string]any) (map[string]any, string, bool) {
	hash, ok := entry["trusted_hash"].(string)
	if !ok || hash == "" || !plainPath(hash) {
		return nil, "", false
	}
	for k, v := range entry {
		switch k {
		case "trusted_hash":
		case "enabled":
			if v != true {
				return nil, "", false
			}
		default:
			return nil, "", false
		}
	}
	return entry, hash, true
}

// spelling returns dir spelled as the source key spells its folder: resolved
// when the source key is resolved, as given otherwise.
func spelling(sourceKey, dir string) string {
	if sourceKey == pathutil.Resolved(sourceKey) {
		return pathutil.Resolved(dir)
	}
	return dir
}

// planAdoption finds the first of roots that Codex trusts with duolog's gate
// approved, and the trust and approvals dir lacks. userHooks is the hooks
// file of the Codex home, whose approvals hold in every folder.
func planAdoption(data []byte, dir string, roots []string, userHooks string, stale map[string]string) (adoption, bool) {
	var cfg adoptConfig
	var settings codexSettings
	if toml.Unmarshal(data, &cfg) != nil || toml.Unmarshal(data, &settings) != nil {
		return adoption{}, false
	}
	dirTrusted, ok := worktreeTrust(cfg, dir)
	if !ok {
		return adoption{}, false
	}
	userState, err := hooksAt(userHooks, settings, stale)
	userGate := err == nil && userState.Trusted && userState.Unproven == ""
	target, haveTarget := readHooksDoc(filepath.Join(dir, ".codex", "hooks.json"))
	for _, root := range roots {
		trustKey := trustedKey(cfg, root)
		if trustKey == "" {
			continue
		}
		p := adoption{dir: dir, from: root}
		if !dirTrusted {
			p.trustKey = spelling(trustKey, dir)
		}
		gate := false
		if haveTarget {
			p.approvals, gate = copyApprovals(cfg, root, dir, target, stale)
		}
		if !gate && !userGate {
			continue
		}
		if p.trustKey == "" && len(p.approvals) == 0 {
			return adoption{}, false
		}
		return p, true
	}
	return adoption{}, false
}

// worktreeTrust reports whether Codex trusts dir already. ok is false when
// the person set another trust level for it, which duolog leaves alone.
func worktreeTrust(cfg adoptConfig, dir string) (trusted, ok bool) {
	for key, project := range cfg.Projects {
		if samePath(key, dir) {
			if project["trust_level"] != "trusted" {
				return false, false
			}
			trusted = true
		}
	}
	return trusted, true
}

// trustedKey returns the key of the projects table that trusts root, or "".
func trustedKey(cfg adoptConfig, root string) string {
	keys := make([]string, 0, len(cfg.Projects))
	for key := range cfg.Projects {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if samePath(key, root) && cfg.Projects[key]["trust_level"] == "trusted" {
			return key
		}
	}
	return ""
}

// copyApprovals lists the approvals of duolog's hooks in root's hooks file
// that dir's hooks file lacks, and reports whether dir then has duolog's
// PreToolUse gate approved.
func copyApprovals(cfg adoptConfig, root, dir string, target hooksDoc, stale map[string]string) ([]approvalCopy, bool) {
	source, ok := readHooksDoc(filepath.Join(root, ".codex", "hooks.json"))
	if !ok {
		return nil, false
	}
	keys := make([]string, 0, len(cfg.Hooks.State))
	for key := range cfg.Hooks.State {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var copies []approvalCopy
	gate := false
	for _, key := range keys {
		c, isGate, ok := copyApproval(cfg, key, root, dir, source, target, stale)
		if !ok {
			continue
		}
		gate = gate || isGate
		if c.key != "" {
			copies = append(copies, c)
		}
	}
	return copies, gate
}

// copyApproval reads the approval key of root's hooks file. ok is true when it
// approves a duolog hook that dir's hooks file has the same at the same place;
// c is then the approval to write for dir, with an empty key when dir has that
// approval already. isGate says it approves duolog's PreToolUse gate.
func copyApproval(cfg adoptConfig, key, root, dir string, source, target hooksDoc, stale map[string]string) (c approvalCopy, isGate, ok bool) {
	path, spelled, position, ok := splitApproval(key)
	event := codexEvents()[spelled]
	if !ok || event == "" || !samePath(path, filepath.Join(root, ".codex", "hooks.json")) {
		return approvalCopy{}, false, false
	}
	entry, hash, ok := approvalEntry(cfg.Hooks.State[key])
	if !ok || stale[key] == hash {
		return approvalCopy{}, false, false
	}
	group, handler := positionOf(position)
	if !sameOwnHandler(event, group, handler, source, target) {
		return approvalCopy{}, false, false
	}
	isGate = event == "PreToolUse" && source.groups[event][group].IsGate(handler)
	newKey := copyKey(path, spelled, position, dir)
	if have, ok := existing(cfg.Hooks.State, newKey); ok {
		return approvalCopy{}, isGate, have["trusted_hash"] == hash
	}
	return approvalCopy{key: newKey, event: event, position: position, hash: hash, entry: entry}, isGate, plainPath(newKey)
}

// copyKey is the approval key that AdoptWorktree writes for dir from the
// approval of event at position in the source hooks file at path.
func copyKey(path, event, position, dir string) string {
	return filepath.Join(spelling(path, dir), ".codex", "hooks.json") + ":" + event + ":" + position
}

// positionOf reads a group:handler position that splitApproval checked.
func positionOf(position string) (group, handler int) {
	g, h, _ := strings.Cut(position, ":")
	group, _ = strconv.Atoi(g)
	handler, _ = strconv.Atoi(h)
	return group, handler
}

// sameOwnHandler reports whether handler h of group g of event is duolog's
// own in the source hooks file, and that group is the same in the target.
func sameOwnHandler(event string, g, h int, source, target hooksDoc) bool {
	if g >= len(source.groups[event]) || g >= len(target.groups[event]) || g >= len(source.raw[event]) || g >= len(target.raw[event]) {
		return false
	}
	return ownHandler(event, source.groups[event][g], h) && reflect.DeepEqual(source.raw[event][g], target.raw[event][g])
}

// existing returns the approval that state holds for the hooks file and
// position of key, under any spelling of the path.
func existing(state map[string]map[string]any, key string) (map[string]any, bool) {
	path, event, position, _ := splitApproval(key)
	for k, v := range state {
		p, e, pos, ok := splitApproval(k)
		if ok && e == event && pos == position && samePath(p, path) {
			return v, true
		}
	}
	return nil, false
}
