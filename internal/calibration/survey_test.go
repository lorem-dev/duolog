package calibration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/profile"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// checkCalls records what a fake check runner was asked to run, and whether
// the tree it ran in held go.mod at that moment.
type checkCalls struct {
	dirs     []string
	commands []string
	hadGoMod bool
}

func recordChecks(calls *checkCalls, exit int, seconds float64) CheckRunner {
	return func(_ context.Context, dir string, commands []string) []journal.Check {
		calls.dirs = append(calls.dirs, dir)
		calls.commands = append(calls.commands, commands...)
		calls.hadGoMod = calls.hadGoMod || exists(filepath.Join(dir, "go.mod"))
		checks := make([]journal.Check, 0, len(commands))
		for _, command := range commands {
			checks = append(checks, journal.Check{Command: command, Exit: exit, Seconds: seconds})
		}
		return checks
	}
}

// fakeChecks never runs a command: every check exits with exit after seconds.
func fakeChecks(exit int, seconds float64) CheckRunner {
	return recordChecks(&checkCalls{}, exit, seconds)
}

func goProject(t *testing.T) string {
	t.Helper()
	root := project(t)
	write(t, root, "go.mod", "module example.invalid/demo\n\ngo 1.24\n")
	write(t, root, "main.go", "package main\n\nfunc main() {}\n")
	write(t, root, "main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestMain(t *testing.T) {}\n")
	write(t, root, ".github/workflows/ci.yml", "on: push\njobs: {}\n")
	commit(t, root, "start")
	return root
}

func TestSurveyGoProject(t *testing.T) {
	ctx := context.Background()
	root := goProject(t)
	s, err := RunSurvey(ctx, root, testConfig(t), fakeChecks(0, 2.5))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Facts.Languages) == 0 || s.Facts.Languages[0] != "go" {
		t.Fatalf("Languages = %v, want go first", s.Facts.Languages)
	}
	want := []string{"go vet ./...", "go test ./..."}
	if !reflect.DeepEqual(s.Facts.Test, want) {
		t.Fatalf("Test = %v, want %v", s.Facts.Test, want)
	}
	if !reflect.DeepEqual(s.Facts.Build, []string{"go build ./..."}) {
		t.Fatalf("Build = %v, want go build ./...", s.Facts.Build)
	}
	if !reflect.DeepEqual(s.Checks["implement"], want) {
		t.Fatalf("Checks[implement] = %v, want %v", s.Checks["implement"], want)
	}
	if s.Checks["docs"] != nil {
		t.Fatalf("Checks[docs] = %v, want nil", s.Checks["docs"])
	}
	if s.CheckSeconds != 5.0 {
		t.Fatalf("CheckSeconds = %v, want 5.0", s.CheckSeconds)
	}
	if s.Files != 4 || s.Commits != 1 {
		t.Fatalf("Files, Commits = %d, %d, want 4, 1", s.Files, s.Commits)
	}
}

func TestSurveyNeverRunsSetup(t *testing.T) {
	ctx := context.Background()
	root := project(t)
	write(t, root, "package.json", `{"scripts": {"test": "node test.js", "postinstall": "node setup.js", "prepare": "node prepare.js"}}`+"\n")
	write(t, root, "test.js", "console.log('ok')\n")
	write(t, root, "Makefile", "setup:\n\tnpm install\n\ntest:\n\tnpm test\n")
	commit(t, root, "start")
	var calls checkCalls
	s, err := RunSurvey(ctx, root, testConfig(t), recordChecks(&calls, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls.commands, []string{"npm test"}) {
		t.Fatalf("recorded commands = %v, want [npm test]", calls.commands)
	}
	for _, command := range append(slices.Clone(s.Facts.Build), s.Facts.Test...) {
		if strings.Contains(command, "install") || strings.Contains(command, "setup") {
			t.Fatalf("survey proposed %q", command)
		}
	}
}

func TestSurveyDropsCheckFailingOnHead(t *testing.T) {
	ctx := context.Background()
	root := goProject(t)
	s, err := RunSurvey(ctx, root, testConfig(t), fakeChecks(1, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	for class, checks := range s.Checks {
		if len(checks) > 0 {
			t.Fatalf("Checks[%s] = %v, want empty", class, checks)
		}
	}
	note := "go test ./... fails on HEAD in a clean export; calibration cannot use it"
	if !slices.Contains(s.Notes, note) {
		t.Fatalf("Notes = %q, want %q", s.Notes, note)
	}
}

// TestSurveyTimesOnlyUsableChecks has go vet fail after 30 s and go test
// pass in 2 s: only the check that stays counts in the check time.
func TestSurveyTimesOnlyUsableChecks(t *testing.T) {
	root := goProject(t)
	s, err := RunSurvey(t.Context(), root, testConfig(t), func(_ context.Context, dir string, commands []string) []journal.Check {
		var out []journal.Check
		for _, c := range commands {
			check := journal.Check{Command: c, Seconds: 2}
			if c == "go vet ./..." {
				check.Exit, check.Seconds = 1, 30
			}
			out = append(out, check)
		}
		return out
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Failed, []string{"go vet ./..."}) || s.CheckSeconds != 2 {
		t.Fatalf("Failed %v CheckSeconds %v, want go vet failed and 2 s", s.Failed, s.CheckSeconds)
	}
}

func TestSurveyRunsInExportNotCheckout(t *testing.T) {
	ctx := context.Background()
	root := goProject(t)
	var calls checkCalls
	if _, err := RunSurvey(ctx, root, testConfig(t), recordChecks(&calls, 0, 1)); err != nil {
		t.Fatal(err)
	}
	// One run per command, all in the same export.
	if len(calls.dirs) != 2 || calls.dirs[0] != calls.dirs[1] {
		t.Fatalf("check runs = %v, want two in one export", calls.dirs)
	}
	if inside(root, calls.dirs[0]) {
		t.Fatalf("checks ran in %s, under the project %s", calls.dirs[0], root)
	}
	if !calls.hadGoMod {
		t.Fatal("the export the checks ran in has no go.mod")
	}
	if _, err := os.Stat(calls.dirs[0]); !os.IsNotExist(err) {
		t.Fatalf("export %s is left behind: %v", calls.dirs[0], err)
	}
}

// An interrupt during the baseline run ends the survey with ctx's error; it
// does not drop the interrupted commands as failing on HEAD.
func TestSurveyStopsOnInterrupt(t *testing.T) {
	root := goProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := func(_ context.Context, _ string, commands []string) []journal.Check {
		cancel()
		var checks []journal.Check
		for _, command := range commands {
			checks = append(checks, journal.Check{Command: command, Exit: 130})
		}
		return checks
	}
	s, err := RunSurvey(ctx, root, testConfig(t), run)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunSurvey interrupted: %+v, %v; want context.Canceled", s, err)
	}
}

// A test command that hangs is stopped at the check time cap and dropped,
// through the real check runner.
func TestSurveyCheckTimeCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses make and sleep")
	}
	root := project(t)
	write(t, root, "Makefile", "test:\n\tsleep 30\n")
	commit(t, root, "start")
	old := checkTimeout
	t.Cleanup(func() { checkTimeout = old })
	checkTimeout = func(config.Calibration) time.Duration { return 300 * time.Millisecond }
	started := time.Now()
	s, err := RunSurvey(context.Background(), root, testConfig(t), profile.RunChecks)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("the survey took %s; the check was not stopped", elapsed)
	}
	note := "make test runs past the check time cap in a clean export; calibration cannot use it"
	if len(s.Checks) != 0 || !slices.Contains(s.Failed, "make test") || !slices.Contains(s.Notes, note) {
		t.Fatalf("checks %v, failed %v, notes %q; want make test dropped with %q", s.Checks, s.Failed, s.Notes, note)
	}
}

// TestSurveyNamesTheTimedOutCheck has go vet fail, go test run until the
// check time cap and npm test come after it. Only go test is named as timed
// out; npm test never ran, so it is "not run", not a failure, and stays out
// of the checks until a rerun of the survey tries it.
func TestSurveyNamesTheTimedOutCheck(t *testing.T) {
	root := goProject(t)
	write(t, root, "package.json", `{"scripts": {"test": "node test.js"}}`+"\n")
	commit(t, root, "add npm test")
	old := checkTimeout
	t.Cleanup(func() { checkTimeout = old })
	checkTimeout = func(config.Calibration) time.Duration { return 200 * time.Millisecond }
	var ran []string
	run := func(ctx context.Context, _ string, commands []string) []journal.Check {
		var checks []journal.Check
		for _, command := range commands {
			check := journal.Check{Command: command, Exit: -1}
			switch {
			case ctx.Err() != nil:
			case command == "go vet ./...":
				ran, check.Exit = append(ran, command), 1
			case command == "go test ./...":
				ran = append(ran, command)
				<-ctx.Done()
			default:
				ran, check.Exit = append(ran, command), 0
			}
			checks = append(checks, check)
		}
		return checks
	}
	s, err := RunSurvey(t.Context(), root, testConfig(t), run)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"go vet ./...", "go test ./..."}) {
		t.Fatalf("ran %v, want go vet and go test only", ran)
	}
	want := []string{
		"go vet ./... fails on HEAD in a clean export; calibration cannot use it",
		"go test ./... runs past the check time cap in a clean export; calibration cannot use it",
		"npm test was not run: the check time cap ran out before it started; run duolog calibrate survey again to try it",
	}
	for _, note := range want {
		if !slices.Contains(s.Notes, note) {
			t.Errorf("Notes %q lack %q", s.Notes, note)
		}
	}
	if !slices.Equal(s.Failed, []string{"go vet ./...", "go test ./..."}) || !slices.Equal(s.NotRun, []string{"npm test"}) || len(s.Checks) != 0 {
		t.Fatalf("Failed %v NotRun %v Checks %v, want go vet and go test failed, npm test not run and no checks", s.Failed, s.NotRun, s.Checks)
	}
	// The shared part lists only commands that passed on HEAD.
	if test := BuildProjectPart(s, Manifest{}, nil).Survey.Test; len(test) != 0 {
		t.Fatalf("project part test commands %v, want none", test)
	}
}

func TestSurveyNeedsGit(t *testing.T) {
	_, err := RunSurvey(context.Background(), t.TempDir(), testConfig(t), fakeChecks(0, 1))
	if err == nil || err.Error() != "calibration needs a Git repository" {
		t.Fatalf("err = %v, want calibration needs a Git repository", err)
	}
}

func TestParseGitVersion(t *testing.T) {
	for out, want := range map[string][2]int{
		"git version 2.40.0\n":                 {2, 40},
		"git version 2.39.3 (Apple Git-146)\n": {2, 39},
		"git version 2.45.1.windows.1\n":       {2, 45},
		"git version 3.0\n":                    {3, 0},
	} {
		major, minor, err := parseGitVersion(out)
		if err != nil || major != want[0] || minor != want[1] {
			t.Fatalf("parseGitVersion(%q) = %d, %d, %v; want %d, %d", out, major, minor, err, want[0], want[1])
		}
	}
	for _, out := range []string{"", "git version\n", "version 2.40.0\n", "git version 2\n", "git version two.forty\n"} {
		if _, _, err := parseGitVersion(out); err == nil {
			t.Fatalf("parseGitVersion(%q) = nil error, want one", out)
		}
	}
}

func TestCheckGitVersion(t *testing.T) {
	for out, ok := range map[string]bool{
		"git version 2.39.3 (Apple Git-146)\n": false,
		"git version 1.9.5\n":                  false,
		"git version 2.40.0\n":                 true,
		"git version 2.45.1.windows.1\n":       true,
	} {
		t.Run(strings.Fields(out)[2], func(t *testing.T) {
			testbin.Install(t, "git", testbin.Scenario{Stdout: out})
			err := CheckGitVersion(context.Background())
			if ok && err != nil {
				t.Fatalf("CheckGitVersion = %v, want nil for %q", err, out)
			}
			if !ok && (err == nil || !strings.Contains(err.Error(), "calibration needs Git 2.40 or newer") || !strings.Contains(err.Error(), strings.Fields(out)[2])) {
				t.Fatalf("CheckGitVersion = %v, want one that needs Git 2.40 or newer and names %s", err, strings.Fields(out)[2])
			}
		})
	}
}

func TestSurveySaveLoadRoundTrip(t *testing.T) {
	dir := stateDir(t)
	run, err := NewRun(dir, testTime)
	if err != nil {
		t.Fatal(err)
	}
	s := Survey{
		Facts:        SurveyFacts{Languages: []string{"go"}, Test: []string{"go test ./..."}},
		Files:        3,
		Commits:      2,
		HistoryDays:  1,
		MedianLines:  12,
		CheckSeconds: 1.5,
		Checks:       map[string][]string{"implement": {"go test ./..."}},
		Notes:        []string{"a note"},
	}
	if err := SaveSurvey(dir, run.ID, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSurvey(dir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, s) {
		t.Fatalf("loaded survey differs:\n got %+v\nwant %+v", loaded, s)
	}
}
