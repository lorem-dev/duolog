package calibration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/usage"
)

// CellsDir is kept in a run's folder; each cell writes CellsDir/<id>/.
const CellsDir = "cells"

// DelegateFunc runs one delegation with the options in ctx; delegate.Run is
// the real one.
type DelegateFunc func(ctx context.Context, cfg *config.Config, s *store.Store, role, taskPath, thread string) (delegate.Outcome, error)

// CellEnv is what every cell of a run shares.
type CellEnv struct {
	Root, StateDir, RunDir, Lead string
	Cfg                          *config.Config
	Store                        *store.Store
	Delegate                     DelegateFunc
	Checks                       CheckRunner
	// ProtectedRoots are the telemetry and balance roots the command
	// resolved; they reach each cell's and judge's worker launch settings.
	ProtectedRoots []string
}

// CellResult is one cell's outcome. result.json holds it without the patch
// and the checks, which go to result.patch and checks.json.
type CellResult struct {
	State  CellState `json:"state"`
	Passed bool      `json:"passed"`
	Patch  []byte    `json:"-"`
	// Hidden is the check run with the hidden tests in place, ClassChecks
	// the one of a task without hidden tests.
	Hidden, ClassChecks []journal.Check `json:"-"`
	Tokens              int64           `json:"tokens"`
	USD                 float64         `json:"usd"`
	Seconds             float64         `json:"seconds"`
	// Input, Cached and Output are the worker's tokens as it reported them,
	// Cached a subset of Input, for the shared rate learner.
	Input  int64  `json:"input,omitempty"`
	Cached int64  `json:"cached,omitempty"`
	Output int64  `json:"output,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Restored are the tests the candidate deleted that were put back as the
	// parent has them before the checks.
	Restored []string `json:"restored,omitempty"`
	// SetupChanged are the files that define how a check runs that the
	// candidate changed; with any, the state is check_setup_changed.
	SetupChanged []string `json:"setup_changed,omitempty"`
}

type cellChecks struct {
	Hidden []journal.Check `json:"hidden,omitempty"`
	Class  []journal.Check `json:"class,omitempty"`
}

// Seams for tests: the isolation check, the cell's time cap and the time cap
// of one check run.
var (
	verifyIsolated = VerifyIsolated
	cellTimeout    = func(c config.Calibration) time.Duration { return time.Duration(c.CellMaxMinutes) * time.Minute }
	checkTimeout   = func(c config.Calibration) time.Duration { return time.Duration(c.CellMaxMinutes) * time.Minute }
)

// CheckLimit is how long one run of a class's checks may take: the cell's
// time cap.
func CheckLimit(c config.Calibration) time.Duration { return checkTimeout(c) }

// outputRoles gives a task the role Filter gave its candidate.
var outputRoles = map[string]string{"code": "implement", "tests": "tests", "docs": "docs"}

// RunCell replays t on the cell's entry in a fresh export of its parent in
// tree slot 0 and runs the class checks with the hidden tests in place. The
// slot is emptied in every case. A tree that is not isolated, before or
// after the run, gives leakage_exposed; a tree Codex does not trust stops
// the cell with an error that says what to trust. Only a tree that is ready,
// isolated and trusted calls start, right before the worker, so a cell
// refused before then is not charged; one start refuses returns
// ErrNotAdmitted. The time cap, a Claude worker's turn cap and a provider
// limit end the cell without checks, the last two as failed_limit, which is
// no result; a check run past the check time cap fails it. The checks run
// on the candidate's tree with the tests it deleted put back; when it
// changed how a check is defined, the cell is check_setup_changed. An
// interrupt, during the worker or the checks, returns ctx's error and saves
// nothing, leaving the cell for the run to rerun.
func RunCell(ctx context.Context, env CellEnv, c Cell, t TaskRecord, start StartFunc) (r CellResult, err error) {

	if env.Cfg == nil || env.Delegate == nil || env.Checks == nil {
		return r, errors.New("calibration cell: no configuration, delegate or check runner")
	}
	if !validID(c.ID) {
		return r, fmt.Errorf("calibration cell %q: not a cell id", c.ID)
	}
	entry, err := config.ParseEntryKey(c.Entry)
	if err != nil {
		return r, fmt.Errorf("calibration cell %s: %w", c.ID, err)
	}
	dir := filepath.Join(env.RunDir, CellsDir, c.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return r, err
	}
	tree, err := TreeSlot(env.Root, 0)
	if err != nil {
		return r, err
	}
	cleanup, err := NewTreeAt(ctx, env.Root, t.Parent, tree)
	if err != nil {
		return r, err
	}
	defer func() { err = errors.Join(err, cleanup()) }()
	if err := verifyIsolated(tree, env.Root); err != nil {
		r.State, r.Reason = CellLeakage, err.Error()
		return r, saveCell(dir, r, false)
	}
	if err := CheckTreeTrust(entry.Agent, tree); err != nil {
		return r, fmt.Errorf("calibration cell %s: %w", c.ID, err)
	}
	taskPath := filepath.Join(dir, "task.md")
	if err := os.WriteFile(taskPath, []byte(cellBrief(env.Cfg, t)), 0600); err != nil {
		return r, err
	}
	before, err := journal.Capture(tree)
	if err != nil {
		return r, err
	}
	if err := StartStep(start); err != nil {
		return r, err
	}

	cellCtx, cancel := context.WithTimeout(ctx, cellTimeout(env.Cfg.Calibration))
	defer cancel()
	target := entry.Target()
	// The turn cap binds a Claude worker; Codex has no flag for it, so there
	// it is the brief's advice and the time cap is the hard stop.
	// The step's budget and wait notice, which its start set: the account
	// gate admits each of the cell's worker runs within them.
	budget, waiting := StepOptions(ctx)
	cellCtx = delegate.WithOptions(cellCtx, delegate.Options{Lead: env.Lead, Entry: &target, WorkRoot: tree, Calibration: true,
		MaxTurns: env.Cfg.Calibration.CellMaxTurns, ProtectedRoots: env.ProtectedRoots, Budget: budget, Waiting: waiting})
	started := time.Now()
	out, runErr := env.Delegate(cellCtx, env.Cfg, env.Store, cellRole(env.Cfg, t), taskPath, "calibration-"+c.ID)
	r.Seconds = time.Since(started).Seconds()
	u := out.Result.Usage
	r.Tokens = u.Input + u.Output
	r.Input, r.Cached, r.Output = u.Input, u.Cached, u.Output
	r.USD, _ = usage.Cost(env.Cfg, entry.Agent, entry.Model, u.Input, u.Cached, u.Output)
	switch {
	case ctx.Err() != nil:
		return r, ctx.Err()
	case out.Result.QuotaEnded:
		r.State, r.Reason = CellFailedLimit, "provider limit"
		return r, saveCell(dir, r, false)
	case errors.Is(cellCtx.Err(), context.DeadlineExceeded):
		r.State, r.Reason = CellDone, "time cap"
		return r, saveCell(dir, r, false)
	case out.Result.TurnsEnded:
		// Only a Claude worker has the cap, so it is no result, as a provider
		// limit is: a failure would count against Claude entries only.
		r.State, r.Reason = CellFailedLimit, "turn cap"
		return r, saveCell(dir, r, false)
	case runErr != nil:
		return r, fmt.Errorf("calibration cell %s: %w", c.ID, runErr)
	case out.RunHere != nil:
		return r, fmt.Errorf("calibration cell %s: entry %s ran in the lead, not as a worker", c.ID, c.Entry)
	}

	if err := verifyIsolated(tree, env.Root); err != nil {
		r.State, r.Reason = CellLeakage, err.Error()
		return r, saveCell(dir, r, false)
	}
	after, err := journal.Capture(tree)
	if err != nil {
		return r, err
	}
	if r.Patch, err = journal.Diff(tree, before, after); err != nil {
		return r, err
	}
	if r.Restored, err = restoreTests(ctx, env.Root, tree, t); err != nil {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		return r, fmt.Errorf("calibration cell %s: deleted tests: %w", c.ID, err)
	}
	classChecks := env.Cfg.Learning.Classes[t.Class].Checks
	if r.SetupChanged, err = setupChanged(ctx, tree, classChecks, t.Hidden); err != nil {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		return r, fmt.Errorf("calibration cell %s: check setup: %w", c.ID, err)
	}
	if err := applyHidden(ctx, env.RunDir, tree, t); err != nil {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		return r, fmt.Errorf("calibration cell %s: hidden tests: %w", c.ID, err)
	}
	checks, timedOut := runChecks(ctx, env.Checks, checkTimeout(env.Cfg.Calibration), tree, classChecks)
	if ctx.Err() != nil {
		// An interrupt also makes the checks fail; that is no result.
		return r, ctx.Err()
	}
	if len(t.Hidden) > 0 {
		r.Hidden = checks
	} else {
		r.ClassChecks = checks
	}
	r.State, r.Passed = CellDone, passed(checks)
	if len(r.SetupChanged) > 0 {
		// The checks ran as the candidate defined them: the person decides.
		r.State = CellSetupChanged
	}
	if timedOut {
		r.Reason = "check time cap"
	}
	return r, saveCell(dir, r, true)
}

// cellBrief is the candidate's task: the statement and the caps, with no
// source, sha or commit message.
func cellBrief(cfg *config.Config, t TaskRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Goal\n\n%s\n\n", demoteHeadings(strings.TrimSpace(t.Statement)))
	b.WriteString("## Read\n\nThe work tree holds the project; read what the change needs.\n\n")
	b.WriteString("## Write\n\n- the files the change needs in the work tree\n\n")
	fmt.Fprintf(&b, "## Limits\n\nFinish within %d minutes and %d tool turns. Work only in the work tree. Do not search for the history of this code.\n\n",
		cfg.Calibration.CellMaxMinutes, cfg.Calibration.CellMaxTurns)
	b.WriteString("## Check\n\nThe change does what the goal asks")
	if checks := cfg.Learning.Classes[t.Class].Checks; len(checks) > 0 {
		b.WriteString(" and these pass:\n\n")
		for _, check := range checks {
			fmt.Fprintf(&b, "- `%s`\n", check)
		}
	} else {
		b.WriteString(".\n")
	}
	return b.String()
}

// demoteHeadings turns each "## " heading of a statement into "### ", so
// the statement cannot open or repeat a section of the task file. Lines in
// a fence stay as they are: the task parser reads them as text already.
func demoteHeadings(statement string) string {
	lines := strings.Split(statement, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if marker := trimmed[:3]; fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence == "" && strings.HasPrefix(line, "## ") {
			lines[i] = "#" + line
		}
	}
	return strings.Join(lines, "\n")
}

// cellRole is the role a task's delegation runs as: the one its output
// gives, else the first role of its class.
func cellRole(cfg *config.Config, t TaskRecord) string {
	if role := outputRoles[t.Features["output"]]; role != "" {
		return role
	}
	if roles := cfg.Learning.Classes[t.Class].Roles; len(roles) > 0 {
		return roles[0]
	}
	return "implement"
}

// applyHidden puts the hidden tests into tree as the reference has them,
// over whatever the candidate left at their paths.
func applyHidden(ctx context.Context, runDir, tree string, t TaskRecord) error {
	patch := filepath.Join(runDir, TasksDir, t.ID, "hidden.patch")
	info, err := os.Stat(patch)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	for _, name := range t.Hidden {
		if clean := path.Clean(name); clean == "." || pathutil.Outside(clean) || pathutil.Windows(clean) {
			return fmt.Errorf("hidden test %q leaves the work tree", name)
		}
		if _, err := run(ctx, tree, "cat-file", "-e", "HEAD:"+name); err == nil {
			if _, err := run(ctx, tree, "checkout", "HEAD", "--", ":(literal)"+name); err != nil {
				return err
			}
		} else if err := os.RemoveAll(filepath.Join(tree, filepath.FromSlash(name))); err != nil {
			return err
		}
	}
	_, err = run(ctx, tree, "apply", patch)
	return err
}

// changedFiles lists the files the candidate changed, added or deleted in
// tree, with their status codes. Files Git ignores, such as an installed
// node_modules or a virtualenv, are not listed.
func changedFiles(ctx context.Context, tree string) (codes map[string]string, err error) {
	status, err := run(ctx, tree, "-c", "core.quotePath=false", "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	codes = map[string]string{}
	for _, record := range strings.Split(string(status), "\x00") {
		if len(record) < 4 {
			continue
		}
		name := record[3:]
		if clean := path.Clean(name); clean == "." || pathutil.Outside(clean) || pathutil.Windows(clean) {
			return nil, fmt.Errorf("changed file %q leaves the work tree", name)
		}
		codes[name] = record[:2]
	}
	return codes, nil
}

// restoreTests puts back, as the parent has them, the tests the candidate
// deleted in tree, so a check cannot pass by losing a test. A test the
// reference deletes too, or a hidden test, stays as it is. It returns the
// paths it put back; the candidate's patch keeps the deletion.
func restoreTests(ctx context.Context, root, tree string, t TaskRecord) ([]string, error) {
	out, err := run(ctx, root, "-c", "core.quotePath=false", "diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", t.Parent, t.Ref, "--")
	if err != nil {
		return nil, err
	}
	reference := strings.Split(string(out), "\x00")
	codes, err := changedFiles(ctx, tree)
	if err != nil {
		return nil, err
	}
	var restored []string
	for name, code := range codes {
		if !strings.Contains(code, "D") || !testFile(name) || slices.Contains(reference, name) || slices.Contains(t.Hidden, name) {
			continue
		}
		if _, err := run(ctx, tree, "checkout", "HEAD", "--", ":(literal)"+name); err != nil {
			return nil, err
		}
		restored = append(restored, name)
	}
	slices.Sort(restored)
	return restored, nil
}

// testConfigFiles are, by name in any folder, files that only configure a
// test runner; testConfigPrefixes start the names of jest and vitest
// configurations, ciPrefixes the paths of CI configuration.
var (
	testConfigFiles    = []string{"pytest.ini", "tox.ini", "conftest.py"}
	testConfigPrefixes = []string{"jest.config.", "vitest.config.", "vitest.workspace."}
	ciPrefixes         = []string{".github/workflows/", ".gitlab-ci.yml", ".circleci/", ".travis.yml", "azure-pipelines.yml",
		"bitbucket-pipelines.yml", "Jenkinsfile", ".buildkite/"}
	makefiles = []string{"GNUmakefile", "makefile", "Makefile"}
)

// setupChanged returns, sorted, the files the candidate changed in tree
// that may define how one of checks runs: any Makefile, the package.json
// scripts block and its jest or vitest settings, the tool sections of
// pyproject.toml and the pytest one of setup.cfg, test runner and CI
// configuration, files under scripts/, and any file a check command names.
// They count whether or not a check reaches them, as a check may call them
// through a file it runs. Dependency declarations, lock files and the
// hidden tests are not among them. The files stay as the candidate left
// them.
func setupChanged(ctx context.Context, tree string, checks, hidden []string) ([]string, error) {
	codes, err := changedFiles(ctx, tree)
	if err != nil {
		return nil, err
	}
	words := commandWords(checks)
	var changed []string
	for name := range codes {
		if slices.Contains(hidden, name) {
			continue
		}
		// Only these files are read: in the others any change counts.
		var before, after []byte
		if slices.Contains([]string{"package.json", "pyproject.toml", "setup.cfg"}, path.Base(name)) {
			if before, err = headBlob(ctx, tree, name); err != nil {
				return nil, err
			}
			if after, err = treeBlob(tree, name); err != nil {
				return nil, err
			}
		}
		if definesCheck(name, before, after, words) {
			changed = append(changed, name)
		}
	}
	slices.Sort(changed)
	return changed, nil
}

// definesCheck reports whether the change of name from before to after, nil
// when absent, may change how a check of words runs. Which make target or
// script a check reaches is not followed: such a file counts either way.
func definesCheck(name string, before, after []byte, words []string) bool {
	base := path.Base(name)
	switch {
	case slices.ContainsFunc(words, func(w string) bool { return path.Clean(w) == name }):
		return true
	case slices.Contains(testConfigFiles, base), slices.ContainsFunc(ciPrefixes, func(p string) bool { return strings.HasPrefix(name, p) }),
		slices.ContainsFunc(testConfigPrefixes, func(p string) bool { return strings.HasPrefix(base, p) }),
		slices.Contains(makefiles, base), path.Ext(base) == ".mk", strings.HasPrefix(name, "scripts/"):
		return true
	case base == "package.json":
		return packageChecksDiffer(before, after)
	case base == "pyproject.toml":
		return !sameParts(before, after, func(doc []byte) (any, error) {
			var v struct {
				Tool any `toml:"tool"`
			}
			err := toml.Unmarshal(doc, &v)
			return v.Tool, err
		})
	case base == "setup.cfg":
		return !sameParts(before, after, func(doc []byte) (any, error) { return iniSection(doc, "tool:pytest"), nil })
	}
	return false
}

// packageChecksDiffer reports whether a package.json change touches its
// scripts block or its jest or vitest settings.
func packageChecksDiffer(before, after []byte) bool {
	type manifest struct {
		Scripts map[string]string `json:"scripts"`
		Jest    json.RawMessage   `json:"jest"`
		Vitest  json.RawMessage   `json:"vitest"`
	}
	read := func(doc []byte) (manifest, bool) {
		var m manifest
		return m, doc == nil || json.Unmarshal(doc, &m) == nil
	}
	prev, okPrev := read(before)
	next, okNext := read(after)
	if !okPrev || !okNext {
		return true
	}
	return !bytes.Equal(prev.Jest, next.Jest) || !bytes.Equal(prev.Vitest, next.Vitest) || !maps.Equal(prev.Scripts, next.Scripts)
}

// commandWords splits check commands into words without shell quotes; a
// word with = also gives the part after it, as in --config=jest.ci.js.
func commandWords(checks []string) []string {
	var words []string
	for _, check := range checks {
		for _, w := range strings.FieldsFunc(check, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }) {
			w = strings.Trim(w, `"'`)
			words = append(words, w)
			if _, value, ok := strings.Cut(w, "="); ok && value != "" {
				words = append(words, value)
			}
		}
	}
	return words
}

// sameParts reports whether before and after give the same part; a document
// part cannot read is the same only when the bytes are.
func sameParts(before, after []byte, part func([]byte) (any, error)) bool {
	a, errA := part(before)
	b, errB := part(after)
	if errA != nil || errB != nil {
		return bytes.Equal(before, after)
	}
	return reflect.DeepEqual(a, b)
}

// iniSection returns the lines of section in an ini document, trimmed, with
// blank lines and comments left out.
func iniSection(doc []byte, section string) []string {
	var lines []string
	in := false
	for _, line := range strings.Split(string(doc), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			in = strings.TrimSpace(line[1:len(line)-1]) == section
		case in && line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ";"):
			lines = append(lines, line)
		}
	}
	return lines
}

// headBlob returns name as tree's commit has it, or nil when it has none.
func headBlob(ctx context.Context, tree, name string) ([]byte, error) {
	if _, err := run(ctx, tree, "cat-file", "-e", "HEAD:"+name); err != nil {
		return nil, nil
	}
	return run(ctx, tree, "cat-file", "blob", "HEAD:"+name)
}

// treeBlob returns name as tree holds it, a link as its target as Git
// stores it, or nil when tree has none.
func treeBlob(tree, name string) ([]byte, error) {
	file := filepath.Join(tree, filepath.FromSlash(name))
	info, err := os.Lstat(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(file)
		return []byte(filepath.ToSlash(target)), err
	case info.IsDir():
		return nil, nil
	}
	return os.ReadFile(file)
}

// saveCell writes result.json and, for a checked cell, result.patch and
// checks.json.
func saveCell(dir string, r CellResult, checked bool) error {
	if checked {
		if err := os.WriteFile(filepath.Join(dir, "result.patch"), r.Patch, 0600); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(dir, "checks.json"), cellChecks{Hidden: r.Hidden, Class: r.ClassChecks}); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(dir, "result.json"), r)
}
