package agentsync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	root     string
	cfg      config.Sync
	o        Options
	from, to dirs
	fromSide string
	rep      Report
}

// Run converts agents and skills from the source side to the target side.
func Run(root string, cfg config.Sync, o Options) (Report, error) {
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
	r := &runner{root: root, cfg: cfg, o: o, fromSide: side, from: sideDirs(side), to: sideDirs(other(side))}
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
		if err != nil {
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
	return unitRoot(src, r.from) == unitRoot(r.o.Path, r.from)
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
	r.removeStale(lock, produced, failed)
}

func (r *runner) apply(lock *Lock, u unit) {
	want := hashBytes(u.content)
	u.entry.Source, u.entry.TargetHash = u.src, want
	path := r.abs(u.target)
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
	case exists && hashBytes(cur) == want:
		if r.o.Check && u.agent {
			if err := parsesAsAgent(u.target, cur); err != nil {
				r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: does not parse: %v (source %s)", u.target, oneLine(err.Error()), u.src))
				return
			}
		}
		lock.Files[u.target] = u.entry
		return
	case exists && !owned:
		r.rep.Conflicts = append(r.rep.Conflicts, fmt.Sprintf("%s: written by hand, not by duolog; left as is (source %s)", u.target, u.src))
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

func (r *runner) agentUnits() []unit {
	ext := ".md"
	if r.fromSide == "codex" {
		ext = ".toml"
	}
	entries, _ := os.ReadDir(r.abs(r.from.agents))
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
		name, err := resolver.Resolve(context.Background(), cfg, config.Target{Agent: "codex", Model: alias})
		if err != nil {
			return "", err
		}
		cache[alias] = name
		return name, nil
	}
}

func (r *runner) skillUnits() []unit {
	entries, _ := os.ReadDir(r.abs(r.from.skills))
	var units []unit
	for _, e := range entries {
		base := r.from.skills + "/" + e.Name()
		if !e.IsDir() || !r.inScope(base) {
			continue
		}
		if _, err := os.Stat(r.abs(base + "/SKILL.md")); err != nil {
			continue
		}
		_ = filepath.WalkDir(r.abs(base), func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
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
	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return ""
		}
		path = rel
	}
	path = filepath.ToSlash(filepath.Clean(path))
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
	if e, ok := lock.Files[rel]; ok {
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
