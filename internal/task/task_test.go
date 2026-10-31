package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}
func document(read string) string {
	return "# Task: example\n\n## Goal\nComplete work.\n\n## Read\n" + read + "\n## Write\n- result.go\n\n## Limits\nStay focused.\n\n## Check\ngo test ./...\n"
}
func TestInlineHoldsTaskAndRanges(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "task.md")
	write(t, p, document("- a.go:12-40"))
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("line-%03d", i))
	}
	write(t, filepath.Join(root, "a.go"), strings.Join(lines, "\n")+"\n")
	b, e := Inline(p, root, 65536)
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	if !strings.Contains(s, "Complete work.") || !strings.Contains(s, strings.Join(lines[11:40], "\n")) || strings.Contains(s, "line-011") || strings.Contains(s, "line-041") {
		t.Fatal(s)
	}
}
func TestInlineOverCapKeepsReference(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "task.md")
	write(t, p, document("- a.go"))
	write(t, filepath.Join(root, "a.go"), strings.Repeat("SECRET", 500))
	b, e := Inline(p, root, 512)
	if e != nil || len(b) > 512 || !strings.Contains(string(b), "a.go") || strings.Contains(string(b), "SECRET") {
		t.Fatalf("%q %v", b, e)
	}
	if _, e = Inline(p, root, 10); e == nil {
		t.Fatal("task itself exceeds cap")
	}
}
func TestInlineRefusesPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "task.md")
	for _, ref := range []string{"../outside.go", filepath.Join(t.TempDir(), "outside.go")} {
		write(t, p, document("- "+ref))
		if _, e := Inline(p, root, 65536); e == nil {
			t.Fatal(ref)
		}
	}
	if _, e := Inline(filepath.Join(t.TempDir(), "task.md"), root, 65536); e == nil {
		t.Fatal("outside task accepted")
	}
}
func TestRejectsTaskMissingSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "task.md")
	valid := document("- a.go")
	write(t, p, valid)
	if e := Validate(p); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{strings.Replace(valid, "## Limits\n", "", 1), strings.Replace(valid, "## Check", "## Extra", 1), valid + "\n## Goal\nAgain", strings.Replace(strings.Replace(valid, "## Read", "## Check", 1), "## Check\ngo test", "## Read\ngo test", 1)} {
		write(t, p, s)
		if Validate(p) == nil {
			t.Fatal(s)
		}
	}
}
func TestSchemaRequiresAllFields(t *testing.T) {
	for _, s := range []string{`"done"`, `"summary"`, `"files"`, `"needs"`, `"required"`, `"additionalProperties":false`} {
		if !strings.Contains(Schema, s) {
			t.Fatal(Schema)
		}
	}
}
