package agentsync

import (
	"context"
	"errors"
	"fmt"
	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
)

// Options selects what one run does.
type Options struct {
	// From is "claude" or "codex": the side that holds the sources. It is
	// required on the first run and must match the lock afterwards.
	From            string
	AgentsOnly      bool
	SkillsOnly      bool
	DryRun          bool
	Check           bool
	Force           bool
	SwitchDirection bool
	// Models maps a source alias to a target alias, from --model opus=terra.
	Models map[string]string
	// Path limits the run to the agent file or skill that holds this path.
	Path string
}

// Report lists what a run did. A run with Conflicts is a failed run.
type Report struct {
	Written, Removed, Conflicts, Warnings []string
}

// CatalogList replaces the call to `codex debug models` when it is not nil.
// Tests set it; the catalog is otherwise read through internal/models.
var CatalogList func(context.Context) ([]models.Entry, string, error)

type dirs struct{ agents, skills string }

var (
	claudeDirs = dirs{agents: ".claude/agents", skills: ".claude/skills"}
	codexDirs  = dirs{agents: ".codex/agents", skills: ".agents/skills"}
)

func sideDirs(side string) dirs {
	if side == "claude" {
		return claudeDirs
	}
	return codexDirs
}

// unit is one file to generate.
type unit struct {
	src, target string
	content     []byte
	mode        os.FileMode
	entry       FileEntry
	agent       bool
	err         error
}

type runner struct {
	root        string
	cfg         config.Sync
	o           Options
	from, to    dirs
	fromSide    string
	scanFailed  bool
	pathWindows bool
	rep         Report
}

// Run converts agents and skills from the source side to the target side.
func Run(root string, cfg config.Sync, o Options) (Report, error) {
	pathWindows := runtime.GOOS == "windows" || pathutil.Windows(o.Path)
	if o.Path != "" {
		o.Path = relative(root, o.Path)
		if o.Path == "" {
			return Report{}, fmt.Errorf("--path must be inside the project")
		}
	}
	mutexPath := filepath.Join(project.StateRoot(root), ".duolog", "sync.lock")
	if _, err := os.Stat(mutexPath); err == nil || !o.DryRun && !o.Check {
		if err := os.MkdirAll(filepath.Dir(mutexPath), 0755); err != nil {
			return Report{}, err
		}
		mutex := flock.New(mutexPath)
		// Eight runs in a row took over two seconds on a slow Windows runner, and a
		// sync that gives up leaves generated files out of date.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		locked, err := mutex.TryLockContext(ctx, 20*time.Millisecond)
		if err != nil || !locked {
			mutex.Close()
			return Report{}, fmt.Errorf("sync lock unavailable: %v", err)
		}
		defer mutex.Close()
	}
	lock, err := LoadLock(root)
	if err != nil {
		return Report{}, err
	}
	side := lock.Direction
	switch {
	case o.From != "" && o.From != "claude" && o.From != "codex":
		return Report{}, fmt.Errorf("--from must be claude or codex, got %q", o.From)
	case side == "" && o.From == "":
		return Report{}, errors.New("no direction chosen yet: run duolog sync --from claude or --from codex")
	case side == "":
		side = o.From
	case o.From != "" && o.From != side:
		if !o.SwitchDirection {
			return Report{}, fmt.Errorf("files were generated from %s; --from %s would reverse that: pass --switch-direction to rebuild the old sources", side, o.From)
		}
		lock = switched(root, lock)
		side = o.From
	}
	lock.Direction = side
	r := &runner{root: root, cfg: cfg, o: o, pathWindows: pathWindows, fromSide: side, from: sideDirs(side), to: sideDirs(other(side))}
	r.sync(&lock)
	if !o.DryRun && !o.Check {
		if err := saveLock(root, lock); err != nil {
			return r.rep, err
		}
	}
	sort.Strings(r.rep.Written)
	sort.Strings(r.rep.Removed)
	sort.Strings(r.rep.Conflicts)
	sort.Strings(r.rep.Warnings)
	return r.rep, nil
}

func other(side string) string {
	if side == "claude" {
		return "codex"
	}
	return "claude"
}

// switched builds the lock of the opposite direction. The old sources become
// generated files, owned as they stand now, so the new run may overwrite them.
func switched(root string, old Lock) Lock {
	next := Lock{Files: map[string]FileEntry{}}
	for key, e := range old.Files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Source)))
		if err != nil || hashBytes(data) != e.SourceHash {
			continue // missing or edited since the last sync: not ours to overwrite
		}
		// With no generated copy, the new run has nothing to rebuild this source
		// from. It stays as a hand-written file instead of being removed as stale.
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(key))); err != nil {
			continue
		}
		next.Files[e.Source] = FileEntry{Source: key, TargetHash: hashBytes(data)}
	}
	return next
}

func (r *runner) abs(rel string) string { return filepath.Join(r.root, filepath.FromSlash(rel)) }

// unitRoot names what a path belongs to: the skill folder, or the agent file.
func unitRoot(rel string, d dirs) string {
	if rest, ok := strings.CutPrefix(rel, d.skills+"/"); ok {
		name, _, _ := strings.Cut(rest, "/")
		return d.skills + "/" + name
	}
	return rel
}

func (r *runner) inScope(src string) bool {
	if r.o.Path == "" {
		return true
	}
	a, b := unitRoot(src, r.from), unitRoot(r.o.Path, r.from)
	if r.pathWindows {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (r *runner) wantKind(target string) (agent, ok bool) {
	agent = strings.HasPrefix(target, r.to.agents+"/")
	skill := strings.HasPrefix(target, r.to.skills+"/")
	if !agent && !skill {
		return false, false
	}
	if (agent && r.o.SkillsOnly) || (skill && r.o.AgentsOnly) {
		return agent, false
	}
	return agent, true
}

func (r *runner) sync(lock *Lock) {
	var units []unit
	if !r.o.SkillsOnly {
		units = append(units, r.agentUnits()...)
	}
	if !r.o.AgentsOnly {
		units = append(units, r.skillUnits()...)
	}
	sort.Slice(units, func(i, j int) bool { return units[i].src < units[j].src })
	produced := map[string]string{}
	failed := map[string]bool{}
	for _, u := range units {
		if u.err != nil {
			failed[u.src] = true
			r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: %v", u.src, oneLine(u.err.Error())))
			continue
		}
		if prev, dup := produced[u.target]; dup {
			failed[u.src] = true
			r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: %s would also be generated from %s", u.src, u.target, prev))
			continue
		}
		produced[u.target] = u.src
		r.apply(lock, u)
	}
	if !r.scanFailed {
		r.removeStale(lock, produced, failed)
	}
}

func (r *runner) apply(lock *Lock, u unit) {
	want := hashBytes(u.content)
	u.entry.Source, u.entry.TargetHash = u.src, want
	path := r.abs(u.target)
	if !r.within(path) {
		r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: outside the generated folders; not written (source %s)", u.target, u.src))
		return
	}
	info, lerr := os.Lstat(path)
	exists := lerr == nil
	if exists && !info.Mode().IsRegular() {
		r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: not a regular file; left as is (source %s)", u.target, u.src))
		return
	}
	var cur []byte
	if exists {
		var err error
		if cur, err = os.ReadFile(path); err != nil {
			r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: %v", u.target, err))
			return
		}
	}
	old, owned := lock.Files[u.target]
	switch {
	case exists && !owned:
		r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: written by hand, not by duolog; left as is (source %s)", u.target, u.src))
		return
	case exists && hashBytes(cur) == want:
		if r.o.Check && u.agent {
			if err := parsesAsAgent(u.target, cur); err != nil {
				r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: does not parse: %v (source %s)", u.target, oneLine(err.Error()), u.src))
				return
			}
		}
		lock.Files[u.target] = u.entry
		return
	case exists && hashBytes(cur) != old.TargetHash && !r.o.Force:
		msg := fmt.Sprintf("%s: edited by hand since the last sync; left as is, edit the source %s or use --force", u.target, u.src)
		if r.o.Check && u.agent {
			if err := parsesAsAgent(u.target, cur); err != nil {
				msg = fmt.Sprintf("%s: does not parse: %v (source %s)", u.target, oneLine(err.Error()), u.src)
			}
		}
		r.rep.Conflicts = append(r.rep.Conflicts, msg)
		return
	}
	r.rep.Written = append(r.rep.Written, u.target)
	if r.o.DryRun || r.o.Check {
		return
	}
	if err := writeAtomic(path, u.content, u.mode); err != nil {
		r.rep.Written = r.rep.Written[:len(r.rep.Written)-1]
		r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: %v", u.target, err))
		return
	}
	lock.Files[u.target] = u.entry
}

// within reports whether path stays under the generated agents or skills folder.
func (r *runner) within(path string) bool {
	path = filepath.Clean(path)
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if info, err := os.Lstat(parent); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if parent == r.root || parent == filepath.Dir(parent) {
			break
		}
	}
	for _, base := range []string{r.abs(r.to.agents), r.abs(r.to.skills)} {
		if strings.HasPrefix(path, filepath.Clean(base)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (r *runner) removeStale(lock *Lock, produced map[string]string, failed map[string]bool) {
	keys := make([]string, 0, len(lock.Files))
	for key := range lock.Files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		e := lock.Files[key]
		if _, ok := r.wantKind(key); !ok || !r.inScope(e.Source) {
			continue
		}
		if _, live := produced[key]; live || failed[e.Source] {
			continue
		}
		path := r.abs(key)
		if !r.within(path) {
			r.rep.Conflicts = append(r.rep.Conflicts, key+": unsafe target parent; not removed")
			continue
		}
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			delete(lock.Files, key)
			continue
		case err != nil:
			r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: %v", key, err))
			continue
		case hashBytes(data) != e.TargetHash && !r.o.Force:
			r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: source %s is gone but the file was edited by hand; left as is, delete it or use --force", key, e.Source))
			continue
		}
		r.rep.Removed = append(r.rep.Removed, key)
		if r.o.DryRun || r.o.Check {
			continue
		}
		if err := os.Remove(path); err != nil {
			r.rep.Removed = r.rep.Removed[:len(r.rep.Removed)-1]
			r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		delete(lock.Files, key)
		r.pruneEmpty(filepath.Dir(path))
	}
}

// pruneEmpty removes folders left empty inside the target skills folder.
func (r *runner) pruneEmpty(dir string) {
	stop := r.abs(r.to.skills)
	for dir != stop && strings.HasPrefix(dir, stop+string(filepath.Separator)) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// readSourceDir treats a file in the way as a scan failure. Windows reports it
// as "path not found", which would otherwise read as a deleted source and
// remove the files generated from it.
func readSourceDir(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return entries, err
	}
	for dir := path; ; dir = filepath.Dir(dir) {
		info, statErr := os.Stat(dir)
		if statErr == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%s is not a directory", dir)
			}
			return nil, err
		}
		if dir == filepath.Dir(dir) {
			return nil, err
		}
	}
}

func (r *runner) agentUnits() []unit {
	ext := ".md"
	if r.fromSide == "codex" {
		ext = ".toml"
	}
	entries, err := readSourceDir(r.abs(r.from.agents))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.scanError(r.from.agents, err)
		return nil
	}
	var units []unit
	var resolve func(string) (string, error)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ext {
			continue
		}
		src := r.from.agents + "/" + e.Name()
		if !r.inScope(src) {
			continue
		}
		data, err := os.ReadFile(r.abs(src))
		if err != nil {
			units = append(units, unit{src: src, err: err})
			continue
		}
		if resolve == nil {
			resolve = r.resolver()
		}
		opts := AgentOpts{Config: r.cfg.Agents, Flags: r.o.Models, Resolve: resolve}
		u := unit{src: src, agent: true, mode: 0o644}
		u.entry.SourceHash = hashBytes(data)
		var a Agent
		if r.fromSide == "claude" {
			if a, err = ParseClaudeAgent(src, data, opts); err == nil {
				u.entry.OriginalName = originalName(data)
				u.content, err = RenderCodexAgent(a)
				u.target = r.to.agents + "/" + a.Name + ".toml"
				u.entry.ResolvedModel = a.Model
			}
		} else if a, err = ParseCodexAgent(src, data, opts); err == nil {
			u.entry.OriginalName = a.Name
			u.content, err = RenderClaudeAgent(a)
			u.target = r.to.agents + "/" + a.Name + ".md"
		}
		u.err = err
		units = append(units, u)
	}
	return units
}

func originalName(md []byte) string {
	header, _, err := splitFrontmatter(md)
	if err != nil {
		return ""
	}
	fields, _ := parseHeader(header)
	return fields["name"]
}

// resolver turns a Codex alias into the newest visible model, through
// internal/models and its cache. It asks once per alias.
func (r *runner) resolver() func(string) (string, error) {
	cache := map[string]string{}
	cacheDir := filepath.Join(r.root, ".duolog", "cache")
	if r.o.DryRun || r.o.Check {
		cacheDir = "" // a run that must not write also leaves the model cache alone
	}
	resolver := models.Resolver{CacheDir: cacheDir, List: CatalogList}
	return func(alias string) (string, error) {
		if name, ok := cache[alias]; ok {
			return name, nil
		}
		cfg := &config.Config{Models: map[string]config.Model{alias: {Family: alias}}}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		name, err := resolver.Resolve(ctx, cfg, config.Target{Agent: "codex", Model: alias})
		if err != nil {
			return "", err
		}
		cache[alias] = name
		return name, nil
	}
}

// ownSkill is the folder name of the skill that `duolog install` writes.
const ownSkill = "duolog"

func (r *runner) skillUnits() []unit {
	entries, err := readSourceDir(r.abs(r.from.skills))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.scanError(r.from.skills, err)
		return nil
	}
	var units []unit
	for _, e := range entries {
		base := r.from.skills + "/" + e.Name()
		if e.Type()&os.ModeSymlink != 0 {
			r.rep.Warnings = append(r.rep.Warnings, base+": symlink source skipped")
			continue
		}
		// duolog installs its own skill on both sides; it is never synced.
		if !e.IsDir() || e.Name() == ownSkill || !r.inScope(base) {
			continue
		}
		if _, err := os.Stat(r.abs(base + "/SKILL.md")); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				r.scanError(base, err)
			}
			continue
		}
		_ = filepath.WalkDir(r.abs(base), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				r.scanError(path, err)
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				r.rep.Warnings = append(r.rep.Warnings, path+": symlink source skipped")
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(r.root, path)
			src := filepath.ToSlash(rel)
			target := r.to.skills + "/" + strings.TrimPrefix(src, r.from.skills+"/")
			u := unit{src: src, target: target, mode: 0o644}
			data, err := os.ReadFile(path)
			if err != nil {
				u.err = err
				units = append(units, u)
				return nil
			}
			if info, err := d.Info(); err == nil {
				u.mode = info.Mode().Perm()
			}
			u.content = data
			u.entry.SourceHash = hashBytes(data)
			if src == base+"/SKILL.md" {
				var warns []string
				u.content, warns, u.err = convertSkill(data, r.cfg.Skills.DropKeys, r.fromSide == "claude")
				for _, w := range warns {
					r.rep.Warnings = append(r.rep.Warnings, src+": "+w)
				}
			}
			units = append(units, u)
			return nil
		})
	}
	return units
}

func parsesAsAgent(target string, data []byte) error {
	if strings.HasSuffix(target, ".toml") {
		var v map[string]any
		return toml.Unmarshal(normalizeNewlines(data), &v)
	}
	header, _, err := splitFrontmatter(data)
	if err != nil {
		return err
	}
	_, err = parseHeader(header)
	return err
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// relative turns a path from a hook into a slash path under root, or "".
func relative(root, path string) string {
	if path == "" {
		return ""
	}
	if pathutil.IsAbs(path) {
		rel, err := pathutil.Relative(root, path)
		if err != nil {
			return ""
		}
		path = rel
	}
	path = pathutil.Clean(path)
	if path == ".." || strings.HasPrefix(path, "../") || path == "." {
		return ""
	}
	return path
}

// Hook brings the generated files up to date after an agent touched path.
// It returns one line for the agent, or "" when there is nothing to act on:
// the path is unrelated, no direction was chosen, or automatic sync is off.
func Hook(root string, cfg config.Sync, path string) string {
	if !cfg.Auto {
		return ""
	}
	lock, err := LoadLock(root)
	if err != nil || lock.Direction == "" {
		return ""
	}
	rel := relative(root, path)
	if rel == "" {
		return ""
	}
	key := rel
	if runtime.GOOS == "windows" || pathutil.Windows(path) {
		for candidate := range lock.Files {
			if strings.EqualFold(candidate, rel) {
				key = candidate
				break
			}
		}
	}
	if e, ok := lock.Files[key]; ok {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err == nil && hashBytes(data) != e.TargetHash {
			return fmt.Sprintf("duolog sync: %s is generated from %s; edit the source instead. The file was left as is.", rel, e.Source)
		}
		return ""
	}
	d := sideDirs(lock.Direction)
	if !strings.HasPrefix(rel, d.agents+"/") && !strings.HasPrefix(rel, d.skills+"/") {
		return ""
	}
	return line(Run(root, cfg, Options{Path: rel}))
}

// SessionStart rebuilds everything that changed since the last run: edits made
// by a shell, and a newer model behind an alias. It is silent unless a file
// was refused.
func SessionStart(root string, cfg config.Sync) string {
	if !cfg.Auto {
		return ""
	}
	lock, err := LoadLock(root)
	if err != nil || lock.Direction == "" {
		return ""
	}
	return line(Run(root, cfg, Options{}))
}

func line(rep Report, err error) string {
	switch {
	case err != nil:
		return "duolog sync: " + oneLine(err.Error())
	case len(rep.Conflicts) == 1:
		return "duolog sync: " + rep.Conflicts[0]
	case len(rep.Conflicts) > 1:
		return fmt.Sprintf("duolog sync: %s (and %d more; run duolog sync)", rep.Conflicts[0], len(rep.Conflicts)-1)
	}
	return ""
}

func (r *runner) scanError(path string, err error) {
	r.scanFailed = true
	r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: source scan failed; no stale files removed: %v", path, err))
}
