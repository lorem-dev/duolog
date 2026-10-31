package artifact

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/store"
)

func fixture(t *testing.T, content string) (*store.Store, string, string) {
	t.Helper()
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "source.txt")
	writeSource(t, path, content)
	return s, root, path
}

func writeSource(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPutIsContentAddressed(t *testing.T) {
	s, _, path := fixture(t, "hello\n")
	first, err := Put(s, path)
	want := fmt.Sprintf("a/%x", sha256.Sum256([]byte("hello\n")))[:14]
	if err != nil || first != want {
		t.Fatalf("Put = %q, %v; want %q", first, err, want)
	}
	other := filepath.Join(filepath.Dir(path), "other.txt")
	writeSource(t, other, "hello\n")
	second, err := Put(s, other)
	if err != nil || second != first {
		t.Fatalf("second Put = %q, %v", second, err)
	}
	entries, err := os.ReadDir(filepath.Join(s.Dir, "artifacts"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("artifacts = %v, %v", entries, err)
	}
}

func TestSnapshotSurvivesSourceChange(t *testing.T) {
	s, root, path := fixture(t, "original\n")
	ref, err := Put(s, path)
	if err != nil {
		t.Fatal(err)
	}
	writeSource(t, path, "changed\n")
	content, stale, err := Read(s, root, ref)
	if err != nil || stale || string(content) != "original\n" {
		t.Fatalf("Read = %q, %v, %v", content, stale, err)
	}
}

func TestReadRange(t *testing.T) {
	var lines strings.Builder
	for i := 1; i <= 45; i++ {
		fmt.Fprintf(&lines, "line %d\n", i)
	}
	s, root, path := fixture(t, lines.String())
	snapshot, err := Put(s, path)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := RepoRef(root, "source.txt:12-40")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(strings.Split(lines.String(), "\n")[11:40], "\n") + "\n"
	for _, r := range []string{ref, snapshot + ":12-40"} {
		content, stale, err := Read(s, root, r)
		if err != nil || stale || string(content) != want {
			t.Fatalf("Read(%q) = %q, %v, %v", r, content, stale, err)
		}
	}
}

func TestRepoRefReportsStale(t *testing.T) {
	s, root, path := fixture(t, "hello\n")
	ref, err := RepoRef(root, "source.txt:1-1")
	if err != nil || ref != "source.txt@ce013625:1-1" {
		t.Fatalf("RepoRef = %q, %v", ref, err)
	}
	content, stale, err := Read(s, root, ref)
	if err != nil || stale || string(content) != "hello\n" {
		t.Fatalf("initial Read = %q, %v, %v", content, stale, err)
	}
	writeSource(t, path, "updated\nsecond\n")
	content, stale, err = Read(s, root, ref)
	if err != nil || !stale || string(content) != "updated\n" {
		t.Fatalf("stale Read = %q, %v, %v", content, stale, err)
	}
}

func TestRefOutsideRootRefused(t *testing.T) {
	s, root, path := fixture(t, "hello\n")
	for _, p := range []string{"../../etc/passwd", `..\..\etc\passwd`, path, "C:/outside", `C:\outside`, "C:outside", `\\server\share\file`, "//server/share/file"} {
		t.Run(p, func(t *testing.T) {
			if _, err := RepoRef(root, p); err == nil {
				t.Error("RepoRef accepted outside path")
			}
			if _, _, err := Read(s, root, p+"@abc"); err == nil {
				t.Error("Read accepted outside path")
			}
		})
	}
}

func TestRangeBeyondEOF(t *testing.T) {
	s, root, _ := fixture(t, "first\nlast")
	for _, tc := range []struct{ spec, want string }{
		{"source.txt:2-40", "last"},
		{"source.txt:3-40", ""},
	} {
		ref, err := RepoRef(root, tc.spec)
		if err != nil {
			t.Fatal(err)
		}
		content, stale, err := Read(s, root, ref)
		if err != nil || stale || string(content) != tc.want {
			t.Fatalf("Read(%q) = %q, %v, %v", ref, content, stale, err)
		}
	}
}

func TestWindowsSeparatorDoesNotChangeRef(t *testing.T) {
	s, root, _ := fixture(t, "hello\n")
	if err := os.Mkdir(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	writeSource(t, filepath.Join(root, "sub", "file.txt"), "hello\n")
	want, err := RepoRef(root, "sub/file.txt:1-2")
	if err != nil {
		t.Fatal(err)
	}
	got, err := RepoRef(root, `sub\file.txt:1-2`)
	if err != nil || got != want || strings.Contains(got, `\`) {
		t.Fatalf("RepoRef = %q, %v; want %q", got, err, want)
	}
	content, stale, err := Read(s, root, strings.ReplaceAll(want, "/", `\`))
	if err != nil || stale || string(content) != "hello\n" {
		t.Fatalf("Read = %q, %v, %v", content, stale, err)
	}
}

func TestInvalidRefsAndRanges(t *testing.T) {
	s, root, _ := fixture(t, "hello\n")
	for _, spec := range []string{"source.txt:0-1", "source.txt:2-1", "source.txt:one-two", "source.txt:1-", ""} {
		if _, err := RepoRef(root, spec); err == nil {
			t.Errorf("RepoRef(%q) succeeded", spec)
		}
	}
	for _, ref := range []string{"", "source.txt", "source.txt@", "source.txt@xyz", "a/../../source.txt", "a/000000000000", "source.txt@ce013625:0-1"} {
		if _, _, err := Read(s, root, ref); err == nil {
			t.Errorf("Read(%q) succeeded", ref)
		}
	}
}
