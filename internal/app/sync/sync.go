package agentsync

import (
	"context"
	"errors"
	"fmt"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/plugin"
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
	// Resolver resolves a Codex alias; without it an alias is an error.
	Resolver ModelResolver
}

// Report lists what a run did. A run with Conflicts is a failed run.
type Report struct {
	Written, Removed, Conflicts, Warnings []string
}

// ModelResolver turns a Codex alias into the newest visible model. cacheDir is
// the folder of the catalog cache, or "" for none.
type ModelResolver interface {
	Resolve(ctx context.Context, cfg *config.Config, cacheDir string, t config.Target) (string, error)
}

// side is one agent's format with the folders it keeps its files in.
type side struct {
	name   string
	format Format
	dirs   Dirs
}

func newSide(formats Formats, name string) side {
	return side{name: name, format: formats[name], dirs: formats[name].Dirs()}
}

// unit is one file to generate. format is the target's, and set for an agent.
type unit struct {
	src, target string
	content     []byte
	mode        os.FileMode
	entry       FileEntry
	agent       bool
	format      Format
	err         error
}

type runner struct {
	root        string
	cfg         config.Sync
	o           Options
	from        side
	to          []side
	scanFailed  bool
	pathWindows bool
	rep         Report
}

// Run converts agents and skills from the source side to every other side.
func Run(root string, formats Formats, cfg config.Sync, o Options) (Report, error) {
	pathWindows := runtime.GOOS == "windows" || pathutil.Windows(o.Path)
	if o.Path != "" {
		o.Path = relative(root, o.Path)
		if o.Path == "" {
			return Report{}, fmt.Errorf("--path must be inside the project")
		}
	}
	release, err := acquire(root, o)
	if err != nil {
		return Report{}, err
	}
	defer release()
	lock, err := LoadLock(root)
	if err != nil {
		return Report{}, err
	}
	lock, dir, err := direction(root, formats, lock, o)
	if err != nil {
		return Report{}, err
	}
	to := targets(formats, dir)
	if len(to) == 0 {
		return Report{}, errors.New("sync needs a second agent to generate files for")
	}
	lock.Direction = dir
	r := &runner{root: root, cfg: cfg, o: o, pathWindows: pathWindows, from: newSide(formats, dir), to: to}
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

// acquire takes the mutex of the sync when the run writes, or when the mutex
// file is there already. The function it returns gives the mutex back.
func acquire(root string, o Options) (release func(), err error) {
	mutexPath := filepath.Join(project.StateRoot(root), ".duolog", "sync.lock")
	if _, err := os.Stat(mutexPath); err != nil && (o.DryRun || o.Check) {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(mutexPath), 0755); err != nil {
		return nil, err
	}
	// Eight runs in a row took over two seconds on a slow Windows runner, and a
	// sync that gives up leaves generated files out of date.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	unlock, err := store.LockFileContext(ctx, mutexPath, 20*time.Millisecond)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("sync lock unavailable: %v", err) //nolint:errorlint // the cause is context text, and the message is the whole error
	}
	return func() { _ = unlock(); cancel() }, nil
}

// direction picks the side that holds the sources: the one the lock names, or
// the one that --from names. A switch rebuilds the lock for the new direction.
func direction(root string, formats Formats, lock Lock, o Options) (Lock, string, error) {
	names := formats.names()
	dir := lock.Direction
	switch {
	case o.From != "" && formats[o.From] == nil:
		return lock, "", fmt.Errorf("--from must be %s, got %q", strings.Join(names, " or "), o.From)
	case dir == "" && o.From == "":
		return lock, "", fmt.Errorf("no direction chosen yet: run duolog sync --from %s", strings.Join(names, " or --from "))
	case dir == "":
		dir = o.From
	case o.From != "" && o.From != dir:
		if !o.SwitchDirection {
			return lock, "", fmt.Errorf("files were generated from %s; --from %s would reverse that: pass --switch-direction to rebuild the old sources", dir, o.From)
		}
		lock = switched(root, lock)
		dir = o.From
	}
	if formats[dir] == nil {
		return lock, "", fmt.Errorf("files were generated from %s, which is not a known agent; pass --from %s with --switch-direction", dir, strings.Join(names, " or --from "))
	}
	return lock, dir, nil
}

// targets returns every side but the source, in order of name.
func targets(formats Formats, source string) []side {
	var to []side
	for _, name := range formats.names() {
		if name != source {
			to = append(to, newSide(formats, name))
		}
	}
	return to
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
func unitRoot(rel string, d Dirs) string {
	if rest, ok := strings.CutPrefix(rel, d.Skills+"/"); ok {
		name, _, _ := strings.Cut(rest, "/")
		return d.Skills + "/" + name
	}
	return rel
}

func (r *runner) inScope(src string) bool {
	if r.o.Path == "" {
		return true
	}
	a, b := unitRoot(src, r.from.dirs), unitRoot(r.o.Path, r.from.dirs)
	if r.pathWindows {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (r *runner) wantKind(target string) (agent, ok bool) {
	var skill bool
	for _, t := range r.to {
		agent = agent || strings.HasPrefix(target, t.dirs.Agents+"/")
		skill = skill || strings.HasPrefix(target, t.dirs.Skills+"/")
	}
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
	sort.SliceStable(units, func(i, j int) bool { return units[i].src < units[j].src })
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
			if err := u.format.Check(u.target, cur); err != nil {
				r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: does not parse: %v (source %s)", u.target, oneLine(err.Error()), u.src))
				return
			}
		}
		lock.Files[u.target] = u.entry
		return
	case exists && hashBytes(cur) != old.TargetHash && !r.o.Force:
		msg := fmt.Sprintf("%s: edited by hand since the last sync; left as is, edit the source %s or use --force", u.target, u.src)
		if r.o.Check && u.agent {
			if err := u.format.Check(u.target, cur); err != nil {
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
	for _, t := range r.to {
		for _, base := range []string{r.abs(t.dirs.Agents), r.abs(t.dirs.Skills)} {
			if strings.HasPrefix(path, filepath.Clean(base)+string(filepath.Separator)) {
				return true
			}
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
	for _, t := range r.to {
		stop := r.abs(t.dirs.Skills)
		for dir != stop && strings.HasPrefix(dir, stop+string(filepath.Separator)) {
			if os.Remove(dir) != nil {
				return
			}
			dir = filepath.Dir(dir)
		}
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
	entries, err := readSourceDir(r.abs(r.from.dirs.Agents))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.scanError(r.from.dirs.Agents, err)
		return nil
	}
	var units []unit
	var resolve func(string) (string, error)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != r.from.dirs.Ext {
			continue
		}
		src := r.from.dirs.Agents + "/" + e.Name()
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
		for _, t := range r.to {
			opts := AgentOpts{Config: r.cfg.Agents, To: t.name, Flags: r.o.Models, Resolve: resolve}
			u := unit{src: src, agent: true, mode: 0o644, format: t.format}
			u.entry.SourceHash = hashBytes(data)
			var a Agent
			if a, err = r.from.format.ParseAgent(src, data, opts); err == nil {
				u.entry.OriginalName = a.Original
				u.entry.ResolvedModel = a.Resolved
				u.content, err = t.format.RenderAgent(a)
				u.target = t.dirs.Agents + "/" + a.Name + t.dirs.Ext
			}
			u.err = err
			units = append(units, u)
		}
	}
	return units
}

// resolver turns a Codex alias into the newest visible model, through
// the Codex resolver of the options and its cache. It asks once per alias.
func (r *runner) resolver() func(string) (string, error) {
	cache := map[string]string{}
	cacheDir := filepath.Join(r.root, ".duolog", "cache")
	if r.o.DryRun || r.o.Check {
		cacheDir = "" // a run that must not write also leaves the model cache alone
	}
	resolver := r.o.Resolver
	return func(alias string) (string, error) {
		if name, ok := cache[alias]; ok {
			return name, nil
		}
		if resolver == nil {
			return "", fmt.Errorf("no model resolver to resolve %q", alias)
		}
		cfg := &config.Config{Models: map[string]config.Model{alias: {Family: alias}}}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		name, err := resolver.Resolve(ctx, cfg, cacheDir, config.Target{Agent: "codex", Model: alias})
		if err != nil {
			return "", err
		}
		cache[alias] = name
		return name, nil
	}
}

func (r *runner) skillUnits() []unit {
	entries, err := readSourceDir(r.abs(r.from.dirs.Skills))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.scanError(r.from.dirs.Skills, err)
		return nil
	}
	var units []unit
	for _, e := range entries {
		base := r.from.dirs.Skills + "/" + e.Name()
		if r.skillSource(e, base) {
			units = append(units, r.skillTree(base)...)
		}
	}
	return units
}

// skillSource reports whether the entry of the skills folder is a skill to sync.
func (r *runner) skillSource(e os.DirEntry, base string) bool {
	if e.Type()&os.ModeSymlink != 0 {
		r.rep.Warnings = append(r.rep.Warnings, base+": symlink source skipped")
		return false
	}
	// duolog installs its own skills on both sides; they are never synced.
	if !e.IsDir() || slices.Contains(plugin.Skills, e.Name()) || !r.inScope(base) {
		return false
	}
	if _, err := os.Stat(r.abs(base + "/SKILL.md")); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.scanError(base, err)
		}
		return false
	}
	return true
}

// skillTree returns the units of every regular file under the skill folder.
func (r *runner) skillTree(base string) []unit {
	var units []unit
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
		units = append(units, r.skillFile(base, path, d)...)
		return nil
	})
	return units
}

// skillFile returns the units that the file of a skill makes, one for each
// target. A file it cannot read or convert makes one unit that carries the
// error.
func (r *runner) skillFile(base, path string, d fs.DirEntry) []unit {
	rel, _ := filepath.Rel(r.root, path)
	src := filepath.ToSlash(rel)
	rest := strings.TrimPrefix(src, r.from.dirs.Skills+"/")
	// The walk skips non-regular entries; a swap to a symlink mid-walk only
	// reads a file the user owns.
	data, err := os.ReadFile(path)
	if err != nil {
		return []unit{{src: src, target: r.to[0].dirs.Skills + "/" + rest, mode: 0o644, err: err}}
	}
	mode := os.FileMode(0o644)
	if info, err := d.Info(); err == nil {
		mode = info.Mode().Perm()
	}
	content, convErr := data, error(nil)
	if src == base+"/SKILL.md" {
		var warns []string
		content, warns, convErr = r.from.format.Skill(data, r.cfg.Skills.DropKeys)
		for _, w := range warns {
			r.rep.Warnings = append(r.rep.Warnings, src+": "+w)
		}
	}
	var units []unit
	for _, t := range r.to {
		u := unit{src: src, target: t.dirs.Skills + "/" + rest, mode: mode, content: content, err: convErr}
		u.entry.SourceHash = hashBytes(data)
		units = append(units, u)
		if convErr != nil {
			break // one refusal per file, however many targets
		}
	}
	return units
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
func Hook(root string, formats Formats, cfg config.Sync, path string, models ModelResolver) string {
	if !cfg.Auto {
		return ""
	}
	lock, err := LoadLock(root)
	if err != nil || lock.Direction == "" || formats[lock.Direction] == nil {
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
	d := formats[lock.Direction].Dirs()
	if !strings.HasPrefix(rel, d.Agents+"/") && !strings.HasPrefix(rel, d.Skills+"/") {
		return ""
	}
	return line(Run(root, formats, cfg, Options{Path: rel, Resolver: models}))
}

// SessionStart rebuilds everything that changed since the last run: edits made
// by a shell, and a newer model behind an alias. It is silent unless a file
// was refused.
func SessionStart(root string, formats Formats, cfg config.Sync, models ModelResolver) string {
	pruneWorktreeLocks(root)
	if !cfg.Auto {
		return ""
	}
	lock, err := LoadLock(root)
	if err != nil || lock.Direction == "" {
		return ""
	}
	return line(Run(root, formats, cfg, Options{Resolver: models}))
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
