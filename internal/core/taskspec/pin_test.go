package taskspec

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// pinTree is a work root with a file, a folder and a link to a folder outside
// it; link is "" where the system cannot make one, as on Windows without the
// privilege.
func pinTree(t *testing.T) (root, outside, link string) {
	t.Helper()
	root, outside = t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "a.go"), "A\n")
	write(t, filepath.Join(outside, "x.go"), "OUTSIDE\n")
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		link = filepath.Join(root, "link")
	}
	return root, outside, link
}

// The paths a worker or a peer names are confined as the os-based Resolve
// confined them, with the same results and error texts.
func TestResolvePins(t *testing.T) {
	root, outside, link := pinTree(t)
	for _, tc := range []struct{ path, name, want, err string }{
		{"a.go", "a.go", filepath.Join(root, "a.go"), ""},
		{filepath.Join(root, "a.go"), "a.go", filepath.Join(root, "a.go"), ""},
		{"missing.go", "missing.go", filepath.Join(root, "missing.go"), ""},
		{"dir", "dir", filepath.Join(root, "dir"), ""},
		{"../outside.go", "", "", "path outside root: " + filepath.Join(filepath.Dir(root), "outside.go")},
		{filepath.Join(outside, "x.go"), "", "", "path outside root: " + filepath.Join(outside, "x.go")},
		{"link/x.go", "", "", "symlink input refused: " + link},
	} {
		if tc.path == "link/x.go" && link == "" {
			continue
		}
		name, got, err := tree(root).Resolve(tc.path)
		if name != tc.name || got != tc.want || errText(err) != tc.err {
			t.Errorf("Resolve(%q) = %q, %q, %v; want %q, %q, %q", tc.path, name, got, err, tc.name, tc.want, tc.err)
		}
	}
	if link == "" {
		t.Log("no symlink on this system; the link case is skipped")
	}
}

// Resolve on fs.FS alone refuses what leaves the root, whatever FS it is.
func TestResolveFSRefuses(t *testing.T) {
	root, _, link := pinTree(t)
	fsys := tree(root).FS
	for _, tc := range []struct{ name, err string }{
		{"a.go", ""},
		{"../outside.go", "path outside root: ../outside.go"},
		{"dir/../../outside.go", "path outside root: dir/../../outside.go"},
		{"/outside/a.go", "path outside root: /outside/a.go"},
		{"link/x.go", "symlink input refused: link"},
	} {
		if tc.name == "link/x.go" && link == "" {
			continue
		}
		got, err := Resolve(fsys, tc.name)
		want := tc.name
		if tc.err != "" {
			want = ""
		}
		if got != want || errText(err) != tc.err {
			t.Errorf("Resolve(%q) = %q, %v; want %q", tc.name, got, err, tc.err)
		}
	}
}

func TestResolveMapFS(t *testing.T) {
	fsys := fstest.MapFS{
		"a.go":      {Data: []byte("A\n")},
		"dir/b.go":  {Data: []byte("B\n")},
		"link":      {Data: []byte("/elsewhere"), Mode: fs.ModeSymlink},
		"dir/inner": {Data: []byte("../a.go"), Mode: fs.ModeSymlink},
	}
	for _, tc := range []struct{ name, err string }{
		{"a.go", ""},
		{"dir/b.go", ""},
		{"missing.go", ""},
		{"dir/missing/x.go", ""},
		{"link", "symlink input refused: link"},
		{"link/x.go", "symlink input refused: link"},
		{"dir/inner", "symlink input refused: dir/inner"},
		{"..", "path outside root: .."},
		{"/a.go", "path outside root: /a.go"},
	} {
		got, err := Resolve(fsys, tc.name)
		if errText(err) != tc.err || (err == nil && got != tc.name) {
			t.Errorf("Resolve(%q) = %q, %v; want %q", tc.name, got, err, tc.err)
		}
	}
}

func TestInlinePins(t *testing.T) {
	root, outside, link := pinTree(t)
	task := filepath.Join(root, "task.md")
	run := func(read string, maxBytes int) (string, string) {
		t.Helper()
		write(t, task, document(read))
		got, err := inline(task, root, maxBytes)
		return string(got), errText(err)
	}
	doc := document("- a.go")
	if got, err := run("- a.go", 65536); got != "<stdin>\n"+doc+"\n<read path=\"a.go\">\nA\n\n</read>\n\n</stdin>\n" || err != "" {
		t.Errorf("a read that fits: %q, %s", got, err)
	}
	// The read does not fit, so it stays a reference.
	if got, err := run("- a.go", len("<stdin>\n")+len(doc)+len("\n</stdin>\n")+10); got != "<stdin>\n"+doc+"\n</stdin>\n" || err != "" {
		t.Errorf("a cut read: %q, %s", got, err)
	}
	if got, err := run("- dir/", 65536); !strings.Contains(got, "Not inlined, could not be read: dir (a directory).") || err != "" {
		t.Errorf("a folder: %q, %s", got, err)
	}
	// A reference outside the root, by "..", by an absolute path, by a
	// Windows drive path or through a link, stops the handoff. The error
	// names the reference as Read cleaned it: Windows paths, and every path
	// on Windows, are lower-cased.
	asRead := func(p string) string { return pathutil.Native(pathutil.Clean(p)) }
	for _, tc := range []struct{ read, err string }{
		{"- ../outside.go", "path outside root: " + filepath.Join(filepath.Dir(root), "outside.go")},
		{"- " + filepath.Join(outside, "x.go"), "path outside root: " + asRead(filepath.Join(outside, "x.go"))},
		{`- C:\Outside\X.go`, "path outside root: " + asRead(`C:\Outside\X.go`)},
		{"- link/x.go", "symlink input refused: " + link},
	} {
		if strings.Contains(tc.read, "link") && link == "" {
			continue
		}
		if got, err := run(tc.read, 65536); got != "" || err != tc.err {
			t.Errorf("%s: %q, %s; want %s", tc.read, got, err, tc.err)
		}
	}
	// So does a task file outside the root or reached through a link.
	other := filepath.Join(outside, "task.md")
	write(t, other, doc)
	if _, err := inline(other, root, 65536); errText(err) != "path outside root: "+other {
		t.Errorf("a task outside the root: %v", err)
	}
	if link != "" {
		if _, err := inline(filepath.Join(link, "task.md"), root, 65536); errText(err) != "symlink input refused: "+link {
			t.Errorf("a task through a link: %v", err)
		}
	}
}

// A task copy in the shared state of another worktree is read from its own
// tree, while its references stay confined to the work root.
func TestInlineReadsCopyFromItsTree(t *testing.T) {
	root, shared, _ := pinTree(t)
	copyPath := filepath.Join(shared, "task-1", "task.md")
	if err := os.Mkdir(filepath.Dir(copyPath), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, copyPath, document("- a.go\n- x.go"))
	got, err := Inline(tree(root), tree(shared), copyPath, 65536)
	if err != nil || !strings.Contains(string(got), "<read path=\"a.go\">\nA\n") || strings.Contains(string(got), "OUTSIDE") {
		t.Fatalf("shared copy: %q, %v", got, err)
	}
	if !strings.Contains(string(got), "x.go (no such file)") {
		t.Fatalf("x.go was read from the copy's tree: %q", got)
	}
	if _, err := Inline(tree(root), tree(root), copyPath, 65536); errText(err) != "path outside root: "+copyPath {
		t.Fatalf("a copy outside its tree: %v", err)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
