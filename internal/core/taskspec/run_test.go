package taskspec

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestRunCommandsUseCheckParser(t *testing.T) {
	// Kills ignoring Run, parsing its prose as commands, or leaking Run into Check.
	data := []byte("## Goal\nx\n## Read\n## Write\n## Limits\n## Run\n- `$ make schema-write`\nUse the generator.\n```sh\ngo run \\\n./scripts/schemagen\n```\n## Check\n`go test ./pkg`\n")
	fsys := fstest.MapFS{"task.md": {Data: data}}
	commands, skipped, err := RunCommands(fsys, "task.md")
	if err != nil || strings.Join(commands, "|") != "make schema-write|go run ./scripts/schemagen" || strings.Join(skipped, "|") != "Use the generator." {
		t.Fatalf("commands=%q skipped=%q err=%v", commands, skipped, err)
	}
	checks, _, err := CheckCommands(fsys, "task.md")
	if err != nil || strings.Join(checks, "|") != "go test ./pkg" {
		t.Fatalf("checks=%q err=%v", checks, err)
	}
}

func TestRenderRunReachesWorkerInput(t *testing.T) {
	// Kills omitting Run from rendered tasks or worker input, or leaving commands unquoted.
	data := renderOK(t, Spec{Goal: "g", Run: []string{"make schema-write"}})
	if !strings.Contains(string(data), "## Run\n\n- `make schema-write`\n") {
		t.Fatalf("task: %s", data)
	}
	fsys := fstest.MapFS{"task.md": {Data: data}}
	commands, skipped, err := RunCommands(fsys, "task.md")
	if err != nil || len(skipped) != 0 || len(commands) != 1 {
		t.Fatalf("commands=%q skipped=%q err=%v", commands, skipped, err)
	}
	tree := Tree{FS: fsys, Dir: t.TempDir()}
	brief, err := Inline(tree, tree, "task.md", 8192)
	if err != nil || !strings.Contains(string(brief), "## Run\n\n- `make schema-write`") {
		t.Fatalf("brief=%s err=%v", brief, err)
	}
}

func TestRunSectionOptionalAndOrdered(t *testing.T) {
	// Kills requiring Run, accepting duplicate Run, or accepting Run after Check.
	base := "## Goal\nx\n## Read\n## Write\n## Limits\n## Check\n"
	for _, level := range []string{"##", "#", "###"} {
		for _, run := range []string{"", "## Run\n- none\n"} {
			data := strings.Replace(base, "## Check", run+"## Check", 1)
			data = strings.ReplaceAll(data, "##", level)
			if err := Validate(fstest.MapFS{"t": {Data: []byte(data)}}, "t"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, data := range []string{base + "## Run\n", strings.Replace(base, "## Check", "## Run\n## Run\n## Check", 1)} {
		if err := Validate(fstest.MapFS{"t": {Data: []byte(data)}}, "t"); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}
