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

// Agents write "# Goal" as often as "## Goal". Those levels name a section
// only when it is the one due next, so other headings stay text.
func TestAcceptsOtherHeadingLevels(t *testing.T) {
	p := filepath.Join(t.TempDir(), "task.md")
	for _, level := range []string{"# ", "### "} {
		text := strings.ReplaceAll(document("- a.go:2-3"), "\n## ", "\n"+level)
		text = strings.Replace(text, "Complete work.\n", "Complete work.\n"+level+"Notes\nKeep it short.\n", 1)
		write(t, p, text)
		if err := Validate(p); err != nil {
			t.Fatalf("%q: %v", level, err)
		}
		refs, err := References(p)
		if err != nil || len(refs) != 1 || refs[0].Path != "a.go" || refs[0].First != 2 {
			t.Fatalf("%q: %+v %v", level, refs, err)
		}
		if got := Annotate(text, "- extra\n"); !strings.Contains(got, level+"Limits\n- extra\n") {
			t.Fatalf("%q annotate:\n%s", level, got)
		}
	}
	write(t, p, strings.ReplaceAll(document("- a.go"), "## ", ""))
	if err := Validate(p); err == nil || !strings.Contains(err.Error(), `"## Goal"`) {
		t.Fatalf("missing sections: %v", err)
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
