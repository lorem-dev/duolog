package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/store"
)

// configBackupSuffix names the copy of config.toml that duolog keeps before
// its first change, as duolog install keeps one of each file it replaces.
const configBackupSuffix = ".duolog.bak"

// configLockSuffix names the lock file next to config.toml that duolog holds
// from reading the config to replacing it, so that two adoptions at once do
// not drop one another's tables.
const configLockSuffix = ".duolog.lock"

// configLockWait bounds the wait for another adoption to finish.
const configLockWait = 10 * time.Second

// adopter holds what AdoptWorktree reads its plan from: the config file, the
// worktree, the roots whose trust it may copy, the user hooks file and the
// approvals duolog install made stale.
type adopter struct {
	path, dir string
	roots     []string
	userHooks string
	stale     map[string]string
}

// configChange is an edit of the config file that a plan made: the new
// bytes, and the lines that say what was written to the file at a path.
type configChange struct {
	edited []byte
	lines  func(path string) []string
}

// configPlan plans an edit of the config file from its bytes. ok is false
// when there is nothing to write or a check fails; err says why when the
// person should know.
type configPlan func(data []byte) (c configChange, ok bool, err error)

// adopt plans the trust and approvals dir lacks and writes them (change).
func (a adopter) adopt() ([]string, error) {
	return a.change(func(data []byte) (configChange, bool, error) {
		p, ok := planAdoption(data, a.dir, a.roots, a.userHooks, a.stale)
		if !ok {
			return configChange{}, false, nil
		}
		if !sameDevLaunchers(p.from, a.dir) {
			return configChange{}, false, fmt.Errorf("the dev launcher scripts/dev/bin/duolog is not the same in %s as in %s, and the hooks run it; approve the hooks yourself (/hooks there) once you have checked it",
				a.dir, p.from)
		}
		if p.trustKey != "" {
			if name, same := sameCodexTree(p.from, a.dir); !same {
				return configChange{}, false, fmt.Errorf("%s is not the same in %s as in %s, and Codex loads it in a folder it trusts; trust the folder yourself (/hooks there) once you have checked it",
					name, a.dir, p.from)
			}
		}
		edited, ok := p.apply(data)
		return configChange{edited: edited, lines: p.lines}, ok, nil
	})
}

// change runs plan on the config file and writes its edit, under the config's
// lock. A first look without the lock means that with nothing to write no
// lock file is made. Codex does not take duolog's lock: a config it changed
// meanwhile is read and planned once more.
func (a adopter) change(plan configPlan) ([]string, error) {
	look := func() ([]byte, configChange, bool, error) {
		data, err := store.ReadFile(a.path)
		if os.IsNotExist(err) {
			return nil, configChange{}, false, nil
		}
		if err != nil {
			return nil, configChange{}, false, err
		}
		c, ok, err := plan(data)
		return data, c, ok, err
	}
	if _, _, ok, err := look(); !ok || err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), configLockWait)
	defer cancel()
	unlock, err := store.LockFileContext(ctx, a.path+configLockSuffix, 20*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", a.path, err)
	}
	defer func() { _ = unlock() }()
	for attempt := 0; ; attempt++ {
		data, c, ok, err := look()
		if !ok || err != nil {
			return nil, err
		}
		written, err := writeConfig(a.path, data, c.edited)
		if errors.Is(err, errConfigChanged) && attempt == 0 {
			continue
		}
		if err != nil {
			return nil, err
		}
		return c.lines(written), nil
	}
}

// treeEntry is one entry of a .codex tree: a folder, or a plain file's bytes.
type treeEntry struct {
	dir  bool
	data []byte
}

// sameCodexTree reports whether the .codex folders of root and dir hold the
// same entries, hooks.json at their top aside, which is compared hook by
// hook: the same names, each a folder in both or a plain file with the same
// bytes in both. Codex loads that folder only where it trusts the project,
// and its config can name any file in it. A .codex absent in both is the
// same. Anything there but a plain file or folder, a link included, and a
// .codex that is itself no folder, differ. name is the path that differs or
// could not be read, relative to its root, such as .codex/rules/x.rules.
func sameCodexTree(root, dir string) (name string, same bool) {
	a, failed, err := codexTree(root)
	if err != nil {
		return failed, false
	}
	b, failed, err := codexTree(dir)
	if err != nil {
		return failed, false
	}
	names := slices.Collect(maps.Keys(a))
	for n := range b {
		if _, ok := a[n]; !ok {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	var differ []string
	for _, n := range names {
		x, inA := a[n]
		y, inB := b[n]
		if inA != inB || x.dir != y.dir || !bytes.Equal(x.data, y.data) {
			differ = append(differ, n)
		}
	}
	// A folder that only one tree has differs, and so does what it holds:
	// the first file named is the more useful one.
	for i, n := range differ {
		if i+1 == len(differ) || !strings.HasPrefix(differ[i+1], n+"/") {
			return n, false
		}
	}
	return "", true
}

// codexTree reads root's .codex folder, keyed by slash paths relative to
// root. failed names the path that could not be read, or is no plain file or
// folder.
func codexTree(root string) (entries map[string]treeEntry, failed string, err error) {
	base := filepath.Join(root, ".codex")
	info, err := os.Lstat(base)
	if os.IsNotExist(err) {
		return map[string]treeEntry{}, "", nil
	}
	if err != nil {
		return nil, ".codex", err
	}
	if !info.IsDir() {
		return nil, ".codex", fmt.Errorf("%s is not a folder", base)
	}
	entries = map[string]treeEntry{}
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		name := filepath.ToSlash(rel)
		failed = name
		switch {
		case err != nil:
			return err
		case name == ".codex/hooks.json":
		case d.Type().IsDir():
			if path == base || instructionFolder(name) {
				break
			}
			entries[name] = treeEntry{dir: true}
		case d.Type().IsRegular() && path != base:
			return addTreeFile(entries, name, path)
		default:
			return fmt.Errorf("%s is not a plain file or folder", path)
		}
		return nil
	})
	if err != nil {
		return nil, failed, err
	}
	return entries, "", nil
}

// addTreeFile adds the plain file at path, named name relative to the root,
// to entries, unless it gives an agent only instructions (instructionsOnlyAgent,
// skillText).
func addTreeFile(entries map[string]treeEntry, name, path string) error {
	data, err := readPlain(path)
	if err != nil {
		return err
	}
	if !instructionsOnlyAgent(name, data) && !skillText(name) {
		entries[name] = treeEntry{data: data}
	}
	return nil
}

// instructionFolder reports whether the folder name, relative to the root,
// is the agents folder or a folder of skills: they only hold files, each
// judged by addTreeFile.
func instructionFolder(name string) bool {
	return name == codexAgentsDir || skillText(name+"/")
}

// codexAgentsDir is the folder of a project's Codex agent files, and
// codexSkillsDir, with its slash, that of its skills.
const (
	codexAgentsDir = ".codex/agents"
	codexSkillsDir = ".codex/skills/"
)

// skillText reports whether the file name, relative to the root, belongs to
// a skill, such as the SKILL.md that duolog sync writes. Codex reads a
// skill's text as instructions, as it reads AGENTS.md, and runs a script of
// it only through its shell, under the sandbox and duolog's gate, as it runs
// any other file of the worktree; neither changes a setting, and trust
// compares neither for the rest of the worktree.
func skillText(name string) bool {
	return strings.HasPrefix(name, codexSkillsDir)
}

// instructionKeys are the keys an agent file may hold and still give its
// agent nothing but instructions and a model: what duolog sync writes.
var instructionKeys = []string{"name", "description", "model", "model_reasoning_effort", "developer_instructions"}

// instructionsOnlyAgent reports whether the file name, relative to the root,
// with data, is an agent file of the agents folder that holds only
// instructionKeys, each a string, as duolog sync writes them. Such a file
// cannot change Codex's sandbox, approvals, servers or hooks; it says what a
// subagent is told, as AGENTS.md in the worktree does, which trust does not
// compare either. So a worktree on a branch that added or changed one keeps
// the trust of its source root. Any other key, a nested table and a file
// that does not parse count as the file is and are compared.
func instructionsOnlyAgent(name string, data []byte) bool {
	dir, file := path.Split(name)
	if dir != codexAgentsDir+"/" || path.Ext(file) != ".toml" {
		return false
	}
	var doc map[string]any
	if toml.Unmarshal(data, &doc) != nil {
		return false
	}
	for key, value := range doc {
		if _, text := value.(string); !text || !slices.Contains(instructionKeys, key) {
			return false
		}
	}
	return true
}

// readPlain reads the plain file at path without following a link or
// blocking on a FIFO or device that took its place after a look (openPlain),
// and checks the file it opened is a plain one.
func readPlain(path string) ([]byte, error) {
	f, err := openPlain(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a plain file", path)
	}
	return io.ReadAll(f)
}

// quote renders s as a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// apply appends the adoption's tables to data, with CRLF line ends when data
// has them. ok is false unless the result reads as data plus exactly those
// tables.
func (p adoption) apply(data []byte) ([]byte, bool) {
	var b bytes.Buffer
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n%s%s%s%s%s\n", commentPrefix, p.dir, commentMiddle, p.from, commentSuffix)
	if p.trustKey != "" {
		fmt.Fprintf(&b, "[projects.%s]\ntrust_level = \"trusted\"\n", quote(p.trustKey))
	}
	for _, a := range p.approvals {
		if p.trustKey != "" || a.key != p.approvals[0].key {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[hooks.state.%s]\ntrusted_hash = %s\n", quote(a.key), quote(a.hash))
		if _, ok := a.entry["enabled"]; ok {
			b.WriteString("enabled = true\n")
		}
	}
	b.WriteString(commentEnd + "\n")
	tail := b.Bytes()
	if bytes.Contains(data, []byte("\r\n")) {
		tail = bytes.ReplaceAll(tail, []byte("\n"), []byte("\r\n"))
	}
	edited := append(slices.Clip(data), tail...)
	var before, after map[string]any
	if toml.Unmarshal(data, &before) != nil || toml.Unmarshal(edited, &after) != nil {
		return nil, false
	}
	if before == nil {
		before = map[string]any{}
	}
	if !p.remove(after, before) || !reflect.DeepEqual(before, after) {
		return nil, false
	}
	return edited, true
}

// remove takes the adoption's tables out of after, checking each holds what
// was planned, and drops a table that only they made and before lacks.
func (p adoption) remove(after, before map[string]any) bool {
	take := func(parents []string, key string, want map[string]any) bool {
		m := after
		var chain []map[string]any
		for _, name := range parents {
			next, ok := m[name].(map[string]any)
			if !ok {
				return false
			}
			chain = append(chain, m)
			m = next
		}
		if !reflect.DeepEqual(m[key], want) {
			return false
		}
		delete(m, key)
		// Drop the tables the addition alone created.
		for i := len(parents) - 1; i >= 0; i-- {
			table, ok := chain[i][parents[i]].(map[string]any)
			if !ok || len(table) > 0 || has(before, parents[:i+1]) {
				break
			}
			delete(chain[i], parents[i])
		}
		return true
	}
	if p.trustKey != "" && !take([]string{"projects"}, p.trustKey, map[string]any{"trust_level": "trusted"}) {
		return false
	}
	for _, a := range p.approvals {
		if !take([]string{"hooks", "state"}, a.key, a.entry) {
			return false
		}
	}
	return true
}

// has reports whether m holds the table at path.
func has(m map[string]any, path []string) bool {
	for _, name := range path {
		next, ok := m[name].(map[string]any)
		if !ok {
			return false
		}
		m = next
	}
	return true
}

// lines say what the adoption wrote to the config file at path.
func (p adoption) lines(path string) []string {
	var out []string
	if p.trustKey != "" {
		out = append(out, fmt.Sprintf("Codex now trusts the linked worktree %s, as it trusts %s (%s)", p.dir, p.from, path))
	}
	for _, a := range p.approvals {
		out = append(out, fmt.Sprintf("Codex now runs duolog's %s hook %s in %s, approved as in %s (%s)", a.event, a.position, p.dir, p.from, path))
	}
	return out
}

// errConfigChanged is a config file that changed while duolog edited it.
var errConfigChanged = errors.New("Codex changed its config.toml meanwhile; nothing written") //nolint:staticcheck // ST1005: Codex is a product name

// beforeReplace runs just before writeConfig compares the config file with
// what it read. Tests change it to edit the file there, as Codex may.
var beforeReplace = func() {}

// writeConfig replaces the config file at path, whose bytes were old, with
// data in one rename, after keeping the first backup. It returns the file
// written, or errConfigChanged when the file no longer holds old.
func writeConfig(path string, old, data []byte) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	mode := info.Mode().Perm()
	if err := keepBackup(path+configBackupSuffix, old, mode); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := writeSynced(tmp, data, mode); err != nil {
		return "", err
	}
	beforeReplace()
	if current, err := store.ReadFile(path); err != nil || !bytes.Equal(current, old) {
		return "", errConfigChanged
	}
	return path, store.Rename(tmp.Name(), path)
}

// writeSynced writes data to the new file f, flushes it to disk, closes it
// and gives it mode.
func writeSynced(f *os.File, data []byte, mode os.FileMode) error {
	_, err := f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(f.Name(), mode)
	}
	return err
}

// keepBackup writes data to path unless a backup is there already: the first
// one holds the file as it was before duolog changed it. An empty file there
// counts as none. The backup is written in full to a temporary file and then
// linked into place, so that a write that fails leaves no partial backup and
// an existing one is never replaced. A link or folder in its place is an
// error.
func keepBackup(path string, data []byte, mode os.FileMode) error {
	if kept, err := keptBackup(path); kept || err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := writeSynced(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if kept, keptErr := keptBackup(path); os.IsExist(err) && kept {
			return keptErr
		}
		return err
	}
	return nil
}

// keptBackup reports whether path holds a backup: a plain file that is not
// empty. An empty one is removed.
func keptBackup(path string) (bool, error) {
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return false, nil
	case err != nil:
		return false, err
	case !info.Mode().IsRegular():
		return false, fmt.Errorf("%s is not a plain file; nothing written", path)
	case info.Size() > 0:
		return true, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return false, nil
}
