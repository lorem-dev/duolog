package taskspec

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

func checksOf(t *testing.T, check string) ([]string, []string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "task.md")
	write(t, p, "## Goal\n\nx\n\n## Read\n\n## Write\n\n## Limits\n\n## Check\n\n"+check)
	commands, skipped, err := CheckCommands(os.DirFS(filepath.Dir(p)), filepath.Base(p))
	if err != nil {
		t.Fatal(err)
	}
	return commands, skipped
}

func TestCheckCommandsReadsCommandsAndNamesTheRest(t *testing.T) {
	commands, skipped := checksOf(t, "Run the package tests only.\n\n`go test ./internal/core/policy/...`\n- `go vet ./internal/...`\n* `make lint` (slow)\n- run the app by hand\n- `$ pytest -q`\n\n```sh\n$ ruff check .\nmypy src\n```\n")
	wantCommands := []string{"go test ./internal/core/policy/...", "go vet ./internal/...", "pytest -q", "ruff check .", "mypy src"}
	if !slices.Equal(commands, wantCommands) {
		t.Errorf("commands = %q, want %q", commands, wantCommands)
	}
	wantSkipped := []string{"Run the package tests only.", "* `make lint` (slow)", "- run the app by hand"}
	if !slices.Equal(skipped, wantSkipped) {
		t.Errorf("skipped = %q, want %q", skipped, wantSkipped)
	}
}

func TestCheckCommandsFenceSkipsCommentsAndJoinsContinuations(t *testing.T) {
	commands, skipped := checksOf(t, "```sh\n# run the tests\ngo test \\\n  ./internal/... \\\n  -count=1\n# lint\nmake lint\n```\n")
	want := []string{"go test ./internal/... -count=1", "make lint"}
	if !slices.Equal(commands, want) || len(skipped) != 0 {
		t.Errorf("commands = %q, skipped = %q; want %q and none", commands, skipped, want)
	}
}

func TestRenderedCheckItemsAreCommands(t *testing.T) {
	data := renderOK(t, Spec{Goal: "g", Check: []string{"go test ./...", "make lint", "run `a` by hand"}})
	commands, skipped, err := CheckCommands(fstest.MapFS{"task.md": {Data: data}}, "task.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"go test ./...", "make lint"}; !slices.Equal(commands, want) {
		t.Errorf("commands = %q, want %q\n%s", commands, want, data)
	}
	if len(skipped) != 1 {
		t.Errorf("skipped = %q, want the item with its own backtick", skipped)
	}
	data = renderOK(t, Spec{Goal: "g"})
	if commands, skipped, _ = CheckCommands(fstest.MapFS{"task.md": {Data: data}}, "task.md"); len(commands)+len(skipped) != 0 {
		t.Errorf("an empty Check gave %q and %q", commands, skipped)
	}
}

func TestCheckCommandsEmptySection(t *testing.T) {
	commands, skipped := checksOf(t, "")
	if len(commands) != 0 || len(skipped) != 0 {
		t.Errorf("got %q and %q for an empty section", commands, skipped)
	}
}

func TestCheckCommandsSkipsEmptyAndMultilineSpans(t *testing.T) {
	commands, skipped := checksOf(t, "- ``\n- `a`  `b`\n")
	if len(commands) != 0 || len(skipped) != 2 {
		t.Errorf("commands %q, skipped %q", commands, skipped)
	}
}
