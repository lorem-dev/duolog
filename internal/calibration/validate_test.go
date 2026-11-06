package calibration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/journal"
)

// sumProject returns a repository whose last commit adds Sum to calc.go and
// a test that calls it, and the candidate of that commit.
func sumProject(t *testing.T) (string, Candidate) {
	t.Helper()
	root := project(t)
	write(t, root, "calc.go", "package calc\n\nfunc One() int { return 1 }\n")
	commit(t, root, "start")
	write(t, root, "calc.go", "package calc\n\nfunc One() int { return 1 }\n\n// Sum adds two numbers.\nfunc Sum(a, b int) int {\n\treturn a + b\n}\n")
	write(t, root, "calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) {\n\tif Sum(1, 2) != 3 {\n\t\tt.Fatal(\"sum\")\n\t}\n}\n")
	ref := commit(t, root, "add sum")
	candidates, _ := collect(t, root, nil)
	c := only(t, candidates, ref)
	if len(c.Hidden) != 1 || c.Hidden[0] != "calc_test.go" {
		t.Fatalf("Hidden = %v, want calc_test.go", c.Hidden)
	}
	return root, c
}

// sumDefined reports whether calc.go in dir declares Sum.
func sumDefined(t *testing.T, dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "calc.go"))
	if err != nil {
		t.Errorf("read calc.go in %s: %v", dir, err)
	}
	return strings.Contains(string(data), "func Sum")
}

// exits is a fake check runner: each call in a tree without Sum takes the
// next exit of parent, each call in a tree with Sum the next of reference.
func exits(t *testing.T, calls *int, parent, reference []int) CheckRunner {
	var p, r int
	return func(_ context.Context, dir string, commands []string) []journal.Check {
		*calls++
		var exit int
		if sumDefined(t, dir) {
			exit, r = reference[r%len(reference)], r+1
		} else {
			exit, p = parent[p%len(parent)], p+1
		}
		var checks []journal.Check
		for _, command := range commands {
			checks = append(checks, journal.Check{Command: command, Exit: exit, Seconds: 1})
		}
		return checks
	}
}

var goChecks = []string{"go test ./..."}

func TestValidateHiddenFailToPass(t *testing.T) {
	root, c := sumProject(t)
	calls := 0
	run := func(_ context.Context, dir string, commands []string) []journal.Check {
		calls++
		// The hidden test calls Sum: it fails to build while Sum is undefined.
		exit := 0
		if exists(filepath.Join(dir, "calc_test.go")) && !sumDefined(t, dir) {
			exit = 1
		}
		return []journal.Check{{Command: commands[0], Exit: exit}}
	}
	verification, reason, err := Validate(context.Background(), root, c, goChecks, time.Minute, run)
	if err != nil {
		t.Fatal(err)
	}
	if verification != "hidden" || reason != "" {
		t.Fatalf("verification %q reason %q, want hidden", verification, reason)
	}
	if calls != 6 {
		t.Fatalf("%d check runs, want 3 on the parent and 3 on the reference", calls)
	}
}

func TestValidateDropsFlaky(t *testing.T) {
	root, c := sumProject(t)
	calls := 0
	verification, reason, err := Validate(context.Background(), root, c, goChecks, time.Minute, exits(t, &calls, []int{1, 0, 1}, []int{0}))
	if err != nil {
		t.Fatal(err)
	}
	if verification != "" || reason != "flaky hidden tests" {
		t.Fatalf("verification %q reason %q, want flaky hidden tests", verification, reason)
	}
}

func TestValidateFailsOnReference(t *testing.T) {
	root, c := sumProject(t)
	calls := 0
	verification, reason, err := Validate(context.Background(), root, c, goChecks, time.Minute, exits(t, &calls, []int{1}, []int{1}))
	if err != nil {
		t.Fatal(err)
	}
	if verification != "" || reason != "checks fail on the reference" {
		t.Fatalf("verification %q reason %q, want checks fail on the reference", verification, reason)
	}
}

func TestValidateCheckOnly(t *testing.T) {
	root, c := sumProject(t)
	c.Hidden, c.HiddenNames = nil, nil
	calls := 0
	verification, reason, err := Validate(context.Background(), root, c, goChecks, time.Minute, exits(t, &calls, []int{1}, []int{0}))
	if err != nil {
		t.Fatal(err)
	}
	if verification != "check" || reason != "" {
		t.Fatalf("verification %q reason %q, want check", verification, reason)
	}
}

func TestValidateJudgeOnlyForDocs(t *testing.T) {
	root, c := sumProject(t)
	c.Hidden, c.HiddenNames = nil, nil
	calls := 0
	pass := exits(t, &calls, []int{0}, []int{0})

	c.Class = "docs"
	verification, reason, err := Validate(context.Background(), root, c, goChecks, time.Minute, pass)
	if err != nil {
		t.Fatal(err)
	}
	if verification != "judge_only" || reason != "" {
		t.Fatalf("docs: verification %q reason %q, want judge_only", verification, reason)
	}

	c.Class = "implement"
	verification, reason, err = Validate(context.Background(), root, c, goChecks, time.Minute, pass)
	if err != nil {
		t.Fatal(err)
	}
	if verification != "" || reason != "no verification" {
		t.Fatalf("implement: verification %q reason %q, want no verification", verification, reason)
	}
}

// An interrupt during the checks ends validation with ctx's error, not with
// a reason that would exclude the task.
func TestValidateStopsOnInterrupt(t *testing.T) {
	root, c := sumProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	run := func(_ context.Context, _ string, commands []string) []journal.Check {
		calls++
		cancel()
		return []journal.Check{{Command: commands[0], Exit: 130}}
	}
	verification, reason, err := Validate(ctx, root, c, goChecks, time.Minute, run)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Validate interrupted: %q %q %v, want context.Canceled", verification, reason, err)
	}
	if calls != 1 {
		t.Fatalf("%d check runs after the interrupt, want 1", calls)
	}
}

// A check that runs past the limit excludes the task with "check time cap";
// the runner gets a context that ends at the limit.
func TestValidateCheckTimeCap(t *testing.T) {
	root, c := sumProject(t)
	run := func(ctx context.Context, _ string, commands []string) []journal.Check {
		<-ctx.Done()
		return []journal.Check{{Command: commands[0], Exit: -1}}
	}
	verification, reason, err := Validate(context.Background(), root, c, goChecks, 100*time.Millisecond, run)
	if err != nil {
		t.Fatal(err)
	}
	if verification != "" || reason != "check time cap" {
		t.Fatalf("verification %q reason %q, want check time cap", verification, reason)
	}
}
