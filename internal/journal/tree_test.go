package journal

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func repo(t *testing.T) string {
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	write(t, dir, "a.txt", "one\ntwo\nthree\n")
	write(t, dir, ".gitignore", "ignored.txt\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestCaptureIncludesUntrackedAndSkipsDuolog(t *testing.T) {
	dir := repo(t)
	write(t, dir, "new.txt", "n")
	write(t, dir, ".duolog/x", "x")
	write(t, dir, "ignored.txt", "i")
	tree, err := Capture(dir)
	if err != nil || tree == "" {
		t.Fatal(tree, err)
	}
	files := run(t, dir, "ls-tree", "-r", "--name-only", tree)
	if want := ".gitignore\na.txt\nnew.txt\n"; files != want {
		t.Fatalf("tree files %q, want %q", files, want)
	}
}

func TestCaptureLeavesIndexUntouched(t *testing.T) {
	dir := repo(t)
	write(t, dir, "new.txt", "n")
	before := run(t, dir, "status", "--porcelain")
	if _, err := Capture(dir); err != nil {
		t.Fatal(err)
	}
	if after := run(t, dir, "status", "--porcelain"); after != before {
		t.Fatalf("%q != %q", after, before)
	}
}

func TestDiffAndNumStatBetweenTrees(t *testing.T) {
	dir := repo(t)
	from, _ := Capture(dir)
	write(t, dir, "a.txt", "one\nTWO\nthree\nfour\n")
	to, err := Capture(dir)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := NumStat(dir, from, to)
	if err != nil || !reflect.DeepEqual(stat, []FileChange{{Path: "a.txt", Added: 2, Removed: 1}}) {
		t.Fatal(stat, err)
	}
	patch, err := Diff(dir, from, to)
	if err != nil {
		t.Fatal(err)
	}
	fresh := repo(t)
	if err := Apply(fresh, patch); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(fresh, "a.txt"))
	if string(got) != "one\nTWO\nthree\nfour\n" {
		t.Fatal(string(got))
	}
}

func TestCaptureOutsideGit(t *testing.T) {
	dir := t.TempDir()
	if tree, err := Capture(dir); tree != "" || err != nil {
		t.Fatal(tree, err)
	}
	if head, err := Head(dir); head != "" || err != nil {
		t.Fatal(head, err)
	}
}
