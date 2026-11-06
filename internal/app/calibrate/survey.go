package calibration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/journal"
	projectroot "github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

// Bounds of the history summary: the survey reads at most this much.
const (
	surveyMaxCommits = 2000
	surveyMaxDays    = 365
)

// CheckRunner runs commands in dir and reports each one's exit and time;
// profile.RunChecks is the real one. It stops the running command once ctx
// ends.
type CheckRunner func(ctx context.Context, dir string, commands []string) []journal.Check

// Survey is what calibrate survey found, kept in RunsDir/<id>/survey.json.
type Survey struct {
	Facts        SurveyFacts         `json:"facts"`
	Files        int                 `json:"files"`             // tracked files at HEAD
	Commits      int                 `json:"commits"`           // first-parent, no merges, bounded
	HistoryDays  int                 `json:"history_days"`      // from the oldest counted commit to HEAD
	MedianLines  int                 `json:"median_lines"`      // changed lines of a counted commit
	CheckSeconds float64             `json:"check_seconds"`     // one baseline run on HEAD of the Facts.Test commands that pass
	Checks       map[string][]string `json:"checks,omitempty"`  // class -> proposed checks
	Failed       []string            `json:"failed,omitempty"`  // commands of Facts.Test that fail on HEAD
	NotRun       []string            `json:"not_run,omitempty"` // commands of Facts.Test the check time cap kept from starting
	Notes        []string            `json:"notes,omitempty"`
}

// CheckedClasses get the test commands as checks; the other default classes
// (docs, review, skeleton, plan_review, debug_multi) are judged without them.
var CheckedClasses = []string{"implement", "debug", "feature", "tests", "mechanical"}

// languages maps a file extension to the language it counts toward. Files
// with other extensions (docs, config, data) are not code.
var languages = map[string]string{
	".go": "go", ".rs": "rust", ".py": "python", ".rb": "ruby", ".php": "php",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "typescript",
	".java": "java", ".kt": "kotlin", ".kts": "kotlin", ".scala": "scala", ".swift": "swift",
	".c": "c", ".h": "c", ".cc": "c++", ".cpp": "c++", ".cxx": "c++", ".hh": "c++", ".hpp": "c++",
	".cs": "c#", ".fs": "f#", ".m": "objective-c", ".dart": "dart", ".lua": "lua", ".zig": "zig",
	".ex": "elixir", ".exs": "elixir", ".erl": "erlang", ".hs": "haskell", ".ml": "ocaml",
	".clj": "clojure", ".r": "r", ".jl": "julia", ".pl": "perl", ".sh": "shell", ".bash": "shell",
	".zsh": "shell", ".ps1": "powershell", ".sql": "sql", ".vue": "vue", ".svelte": "svelte",
}

// RunSurvey reads the project at HEAD without changing it: tracked files,
// manifests and a bounded history. It runs the proposed test commands once
// in an export of HEAD and drops those that fail there. It never installs
// dependencies or runs a setup script.
func RunSurvey(ctx context.Context, root string, cfg *config.Config, run CheckRunner) (Survey, error) {
	head, err := surveyHead(ctx, root)
	if err != nil {
		return Survey{}, err
	}
	files, err := trackedFiles(ctx, root, head)
	if err != nil {
		return Survey{}, err
	}
	s := Survey{Files: len(files)}
	s.Facts.Languages, s.Facts.Components = codeShares(files)
	s.Facts.Build, s.Facts.Test, err = proposeCommands(ctx, root, head, files)
	if err != nil {
		return Survey{}, err
	}
	if err := s.readHistory(ctx, root, head); err != nil {
		return Survey{}, err
	}

	usable := slices.Clone(s.Facts.Test)
	if len(s.Facts.Test) == 0 {
		s.Notes = append(s.Notes, "no test command found; classes get no checks")
	} else {
		dir, cleanup, err := NewTree(ctx, root, head)
		if err != nil {
			return Survey{}, err
		}
		checks, timedOut, notRun := s.baseline(ctx, run, checkTimeout(cfg.Calibration), dir)
		if err := errors.Join(ctx.Err(), cleanup()); err != nil {
			return Survey{}, err
		}
		// Only the commands calibration keeps run in later trees, so only
		// their time goes into the estimates.
		for i, check := range checks {
			if check.Exit == 0 {
				s.CheckSeconds += check.Seconds
				continue
			}
			problem := " fails on HEAD"
			if timedOut && i == len(checks)-1 {
				// Only the last command ran when the cap ended.
				problem = " runs past the check time cap"
			}
			usable = slices.DeleteFunc(usable, func(c string) bool { return c == check.Command })
			s.Failed = append(s.Failed, check.Command)
			s.Notes = append(s.Notes, check.Command+problem+" in a clean export; calibration cannot use it")
		}
		// A command the cap kept from starting is neither usable nor failed;
		// it stays in the test commands for the next survey.
		for _, command := range notRun {
			usable = slices.DeleteFunc(usable, func(c string) bool { return c == command })
			s.NotRun = append(s.NotRun, command)
			s.Notes = append(s.Notes, command+" was not run: the check time cap ran out before it started; run duolog calibrate survey again to try it")
		}
	}
	if len(usable) > 0 {
		s.Checks = map[string][]string{}
		for _, class := range CheckedClasses {
			if _, ok := cfg.Learning.Classes[class]; ok {
				s.Checks[class] = slices.Clone(usable)
			}
		}
	}
	return s, nil
}

// baseline runs the test commands in dir one at a time, all under one time
// cap, and stops at the command the cap ended or at an interrupt. It returns
// the checks of the commands that ran, whether the cap ended the last of
// them before it passed, and the commands it did not start.
func (s *Survey) baseline(ctx context.Context, run CheckRunner, limit time.Duration, dir string) (checks []journal.Check, timedOut bool, notRun []string) {
	capped, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for i, command := range s.Facts.Test {
		if capped.Err() != nil {
			return checks, false, s.Facts.Test[i:]
		}
		got := run(capped, dir, []string{command})
		checks = append(checks, got...)
		if ctx.Err() == nil && errors.Is(capped.Err(), context.DeadlineExceeded) && !passed(got) {
			return checks, true, s.Facts.Test[i+1:]
		}
	}
	return checks, false, nil
}

// CheckGitVersion stops calibration on a Git older than 2.40, which lacks
// git check-attr --source.
func CheckGitVersion(ctx context.Context) error {
	out, err := projectroot.Git(ctx, nil, "version")
	if err != nil {
		return fmt.Errorf("git version: %w", err)
	}
	major, minor, err := parseGitVersion(string(out))
	if err != nil {
		return err
	}
	if major < 2 || major == 2 && minor < 40 {
		return fmt.Errorf("calibration needs Git 2.40 or newer for git check-attr --source; found %s", strings.Fields(string(out))[2])
	}
	return nil
}

// parseGitVersion reads major and minor from git version output, such as
// "git version 2.39.3 (Apple Git-146)" or "git version 2.45.1.windows.1".
func parseGitVersion(out string) (major, minor int, err error) {
	bad := fmt.Errorf("cannot read the Git version from %q", strings.TrimSpace(out))
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return 0, 0, bad
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return 0, 0, bad
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	if errMajor != nil || errMinor != nil {
		return 0, 0, bad
	}
	return major, minor, nil
}

// surveyHead resolves HEAD, so every read and the export see one commit.
func surveyHead(ctx context.Context, root string) (string, error) {
	if _, err := run(ctx, root, "rev-parse", "--git-dir"); err != nil {
		return "", errors.New("calibration needs a Git repository")
	}
	out, err := run(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", errors.New("calibration needs a commit on HEAD")
	}
	return strings.TrimSpace(string(out)), nil
}

func trackedFiles(ctx context.Context, root, head string) ([]string, error) {
	out, err := run(ctx, root, "ls-tree", "-r", "-z", "--name-only", head)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" && !calibrationState(name) {
			files = append(files, name)
		}
	}
	return files, nil
}

// codeShares returns the three languages with the most tracked files and
// the top-level directories that hold code.
func codeShares(files []string) (top []string, components []string) {
	counts := map[string]int{}
	dirs := map[string]bool{}
	for _, name := range files {
		language, ok := languages[strings.ToLower(path.Ext(name))]
		if !ok {
			continue
		}
		counts[language]++
		if first, _, nested := strings.Cut(name, "/"); nested && !strings.HasPrefix(first, ".") &&
			first != "vendor" && first != "node_modules" {
			dirs[first] = true
		}
	}
	for language := range counts {
		top = append(top, language)
	}
	slices.SortFunc(top, func(a, b string) int {
		if counts[a] != counts[b] {
			return counts[b] - counts[a]
		}
		return strings.Compare(a, b)
	})
	if len(top) > 3 {
		top = top[:3]
	}
	for dir := range dirs {
		components = append(components, dir)
	}
	slices.Sort(components)
	return top, components
}

// proposeCommands applies the marker rules in order. Every command is a
// build or test run; none installs dependencies or runs setup, and a
// package.json or Makefile contributes only its test entry point.
func proposeCommands(ctx context.Context, root, head string, files []string) (build, test []string, err error) {
	has := map[string]bool{}
	for _, name := range files {
		has[name] = true
	}
	matched := false
	if has["go.mod"] {
		matched = true
		build = append(build, "go build ./...")
		test = append(test, "go vet ./...", "go test ./...")
	}
	if has["Cargo.toml"] {
		matched = true
		build = append(build, "cargo build")
		test = append(test, "cargo test")
	}
	if has["package.json"] {
		data, err := run(ctx, root, "cat-file", "blob", head+":package.json")
		if err != nil {
			return nil, nil, err
		}
		var manifest struct {
			Scripts map[string]any `json:"scripts"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			if script, ok := manifest.Scripts["test"].(string); ok && strings.TrimSpace(script) != "" {
				matched = true
				test = append(test, "npm test")
			}
		}
	}
	if has["pyproject.toml"] || has["pytest.ini"] || has["setup.cfg"] {
		matched = true
		test = append(test, "python -m pytest")
	}
	if !matched {
		for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
			if !has[name] {
				continue
			}
			data, err := run(ctx, root, "cat-file", "blob", head+":"+name)
			if err != nil {
				return nil, nil, err
			}
			if makeTarget(string(data), "test") {
				test = append(test, "make test")
			}
			break
		}
	}
	return build, test, nil
}

// makeTarget reports whether a Makefile defines target as a rule, not as a
// variable such as "test := x".
func makeTarget(makefile, target string) bool {
	for _, line := range strings.Split(makefile, "\n") {
		if line == "" || line[0] == '\t' || line[0] == ' ' || line[0] == '#' {
			continue
		}
		names, rest, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(rest, "=") || strings.ContainsAny(names, "=") {
			continue
		}
		if slices.Contains(strings.Fields(names), target) {
			return true
		}
	}
	return false
}

// readHistory counts the first-parent commits without merges of the last
// surveyMaxDays days, at most surveyMaxCommits, and the median size of one.
func (s *Survey) readHistory(ctx context.Context, root, head string) error {
	out, err := run(ctx, root, "-c", "core.quotePath=false", "log", "--first-parent", "--no-merges",
		"--max-count="+strconv.Itoa(surveyMaxCommits), "--since="+strconv.Itoa(surveyMaxDays)+".days",
		"--numstat", "--no-renames", "--format=%x1e%ct", head)
	if err != nil {
		return err
	}
	var sizes []int
	var newest, oldest time.Time
	for _, record := range strings.Split(string(out), "\x1e") {
		stamp, stat, _ := strings.Cut(record, "\n")
		seconds, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64)
		if err != nil {
			continue
		}
		when := time.Unix(seconds, 0)
		if newest.IsZero() || when.After(newest) {
			newest = when
		}
		if oldest.IsZero() || when.Before(oldest) {
			oldest = when
		}
		size := 0
		for _, f := range numstat(stat) {
			size += f.Added + f.Removed
		}
		sizes = append(sizes, size)
	}
	s.Commits = len(sizes)
	if s.Commits == 0 {
		return nil
	}
	s.HistoryDays = max(1, int(math.Ceil(newest.Sub(oldest).Hours()/24)))
	slices.Sort(sizes)
	if n := len(sizes); n%2 == 1 {
		s.MedianLines = sizes[n/2]
	} else {
		s.MedianLines = (sizes[n/2-1] + sizes[n/2]) / 2
	}
	if s.Commits == surveyMaxCommits {
		s.Notes = append(s.Notes, fmt.Sprintf("history summary stops at %d commits", surveyMaxCommits))
	}
	return nil
}

func surveyPath(stateDir, runID string) string {
	return filepath.Join(RunsDir(stateDir), runID, "survey.json")
}

// SaveSurvey replaces survey.json of the run through a temporary file.
func SaveSurvey(stateDir, runID string, s Survey) error {
	if runID == "" || runID != filepath.Base(runID) || runID == "." || runID == ".." {
		return fmt.Errorf("calibration run %q: not a run id", runID)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	target := surveyPath(stateDir, runID)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".survey-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return err
	}
	return store.Rename(tmp.Name(), target)
}

func LoadSurvey(stateDir, runID string) (Survey, error) {
	if runID == "" || runID != filepath.Base(runID) || runID == "." || runID == ".." {
		return Survey{}, fmt.Errorf("calibration run %q: not a run id", runID)
	}
	data, err := os.ReadFile(surveyPath(stateDir, runID))
	if err != nil {
		return Survey{}, err
	}
	var s Survey
	if err := json.Unmarshal(data, &s); err != nil {
		return Survey{}, fmt.Errorf("calibration run %s: survey.json: %w", runID, err)
	}
	return s, nil
}
