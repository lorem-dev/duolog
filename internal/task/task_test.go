package task

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
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

// A Read reference that cannot be read stays in the task with its cause, so
// the worker knows the evidence is missing instead of starting without it.
func TestInlineNamesUnreadableReferences(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "task.md")
	write(t, p, document("- missing.go\n- short.go:2-9\n- dir/\n- ok.go"))
	write(t, filepath.Join(root, "short.go"), "one\ntwo\nthree\n")
	write(t, filepath.Join(root, "ok.go"), "OK_CONTENT\n")
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	b, e := Inline(p, root, 65536)
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	if !strings.Contains(s, "OK_CONTENT") {
		t.Fatal(s)
	}
	for _, want := range []string{"Not inlined", "missing.go (no such file)", "short.go:2-9 (the range ends past line 3)", "dir (a directory)"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, root) {
		t.Errorf("the note names the work root:\n%s", s)
	}
}

// Prose in Read names no file, so the worker is not told to check files
// nobody named. A first word that does not look like a path is prose unless
// it is a file in the work root, as Makefile is. A comma after a path is not
// part of it.
func TestReferencesSkipProse(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "task.md")
	write(t, filepath.Join(root, "Makefile"), "MAKE_CONTENT\nsecond\n")
	write(t, filepath.Join(root, "LICENSE"), "LICENSE_CONTENT\n")
	write(t, p, document("- the notes the lead wrote last week\n- docs/a.md, docs/b.md\n- `notes`\n- a.go:2-3, the loop\n- review.md and the code\n"+
		"- Makefile\n- LICENSE, the terms\n- Makefile:1-1\n- Changelog of the release"))
	refs, err := References(p, root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, fmt.Sprintf("%s:%d-%d", r.Path, r.First, r.Last))
	}
	want := []string{"docs/a.md:0-0", "notes:0-0", "a.go:2-3", "review.md:0-0", "Makefile:0-0", "LICENSE:0-0", "Makefile:1-1"}
	for i := range want {
		// Reference paths are cleaned, which folds case where the file
		// system ignores it.
		want[i] = pathutil.Clean(want[i])
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("refs %q, want %q", got, want)
	}
	b, err := Inline(p, root, 65536)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "the (") || strings.Contains(s, "docs/a.md, (") || strings.Contains(s, "Changelog (") {
		t.Fatalf("prose reported as a file:\n%s", s)
	}
	for _, want := range []string{`<read path="` + pathutil.Clean("Makefile") + `">` + "\nMAKE_CONTENT\nsecond\n", `<read path="` + pathutil.Clean("LICENSE") + `">` + "\nLICENSE_CONTENT\n", `<read path="` + pathutil.Clean("Makefile") + `:1-1">` + "\nMAKE_CONTENT\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
}

// Inlining keeps no content that cannot fit: references far over the limit
// are not read into memory, one copy each, before the budget is checked.
func TestInlineHoldsNoContentThatCannotFit(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "task.md")
	const size = 4 << 20
	write(t, filepath.Join(root, "large.log"), strings.Repeat("0123456789abcde\n", size/16))
	var read strings.Builder
	for range 16 {
		read.WriteString("- large.log\n- large.log:1-200000\n")
	}
	read.WriteString("- small.go\n")
	write(t, filepath.Join(root, "small.go"), "SMALL\n")
	write(t, p, document(read.String()))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	b, err := Inline(p, root, 65536)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "SMALL") || len(b) > 65536 {
		t.Fatalf("%d bytes:\n%.400s", len(b), b)
	}
	// Holding every read would allocate more than 100 MiB; streaming the
	// ranges stays near one file's size, whatever the runtime's growth steps.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2*size {
		t.Fatalf("Inline allocated %d bytes for a 64 KiB handoff", allocated)
	}
}

// A range counts lines as before: a line longer than a read buffer is one
// line, and the last line needs no newline.
func TestRangeReadsLongLinesAndLastLine(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 10000)
	write(t, filepath.Join(root, "a.txt"), "one\n"+long+"\n"+long)
	write(t, filepath.Join(root, "empty.txt"), "")
	for _, tc := range []struct {
		ref         Reference
		want, cause string
	}{
		{Reference{Path: "a.txt", First: 2, Last: 2}, long + "\n", ""},
		{Reference{Path: "a.txt", First: 2, Last: 3}, long + "\n" + long, ""},
		{Reference{Path: "a.txt", First: 1, Last: 1}, "one\n", ""},
		{Reference{Path: "a.txt", First: 3, Last: 4}, "", "the range ends past line 3"},
		{Reference{Path: "empty.txt", First: 1, Last: 1}, "", "the range ends past line 0"},
	} {
		got, err := tc.ref.Text(root)
		if tc.cause != "" {
			if err == nil || readCause(err) != tc.cause {
				t.Errorf("%+v: %v, want %q", tc.ref, err, tc.cause)
			}
			continue
		}
		if err != nil || string(got) != tc.want {
			t.Errorf("%+v: %d bytes, %v", tc.ref, len(got), err)
		}
	}
	// A range over the limit is not kept, but a range past the end is still
	// an error.
	if got, fits, err := (Reference{Path: "a.txt", First: 2, Last: 3}).read(root, 100); got != nil || fits || err != nil {
		t.Errorf("over the limit: %d bytes, fits %v, %v", len(got), fits, err)
	}
	if _, _, err := (Reference{Path: "a.txt", First: 2, Last: 9}).read(root, 100); err == nil {
		t.Error("a range past the end fits")
	}
}

// A task that alone exceeds the limit fails before any reference is judged
// or opened.
func TestInlineRefusesOversizedTaskBeforeReferences(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "task.md")
	write(t, p, document("- a.go\n- ../outside.go"))
	write(t, filepath.Join(root, "a.go"), "A\n")
	judged := 0
	_, err := Inline(p, root, 64, func(string) bool { judged++; return true })
	if err == nil || !strings.Contains(err.Error(), "task exceeds inline_max_bytes") || judged != 0 {
		t.Fatalf("err %v, %d references judged", err, judged)
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
		refs, err := References(p, filepath.Dir(p))
		if err != nil || len(refs) != 1 || refs[0].Path != "a.go" || refs[0].First != 2 {
			t.Fatalf("%q: %+v %v", level, refs, err)
		}
		if got := Annotate(text, "- extra\n"); !strings.Contains(got, level+"Limits\n- extra\n") {
			t.Fatalf("%q annotate:\n%s", level, got)
		}
	}
	// A task with "## " sections keeps other headings as text, as 1.0 did.
	for _, text := range []string{
		strings.Replace(document("- a.go"), "Complete work.\n", "Complete work.\n# Read\nthe log first.\n", 1),
		strings.Replace(document("- a.go"), "Stay focused.\n", "Stay focused.\n### Check\nno network\n", 1),
	} {
		write(t, p, text)
		if err := Validate(p); err != nil {
			t.Fatalf("a 1.0 task fails: %v\n%s", err, text)
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

func TestWritePathsTakesPathsNotProse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(p, []byte("## Goal\nx\n## Read\n## Write\n- `internal/a.go`: the limiter\n- docs/\n- cmd/x.go:10-20\n- the changelog entry\n* internal/**/*_test.go\nprose line.go\n## Limits\n## Check\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := WritePaths(p)
	if err != nil || strings.Join(got, " ") != "internal/a.go docs cmd/x.go internal/**/*_test.go" {
		t.Fatal(got, err)
	}
}
