package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

func TestWriteTaskFileUnderWorkRoot(t *testing.T) {
	work := t.TempDir()
	task := mcp.Task{Goal: "Fix it.", Read: []string{"a.md:1-3"}, Write: []string{"b.go"}, Limits: []string{"small"}, Check: []string{"go test ./..."}}
	rel, err := writeTaskFile(work, "d-abcdef", task)
	if err != nil {
		t.Fatal(err)
	}
	if rel != ".duolog/tasks/d-abcdef/task.md" {
		t.Fatalf("rel = %q", rel)
	}
	got, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := taskspec.Render(taskspec.Spec(task))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("file = %q, want %q", got, want)
	}
	if err := taskspec.Validate(os.DirFS(work), rel); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTaskFileRefusesBadInput(t *testing.T) {
	work := t.TempDir()
	if _, err := writeTaskFile(work, "d-abcdef", mcp.Task{Goal: "g\n## Write\nx"}); err == nil {
		t.Fatal("a heading in the goal was accepted")
	}
	for _, name := range []string{"", "../x", "a/b", `a\b`, "."} {
		if _, err := writeTaskFile(work, name, mcp.Task{Goal: "g"}); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(work, ".duolog")); err == nil {
		t.Fatal("a refused task left a folder behind")
	}
}
