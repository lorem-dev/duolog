package artifact

import (
	"github.com/lorem-dev/duolog/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsArtifactPathsOnEveryHost(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "file.txt"), []byte("one\r\ntwo\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ref, err := RepoRef(root, `SRC\FILE.TXT:2-2`)
	if err != nil || !strings.HasPrefix(ref, "src/file.txt@") {
		t.Fatal(ref, err)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ref = strings.ReplaceAll(strings.ToUpper(strings.Split(ref, "@")[0]), "/", `\`) + "@" + strings.Split(ref, "@")[1]
	content, stale, err := Read(s, root, ref)
	if err != nil || stale || string(content) != "two\r\n" {
		t.Fatal(string(content), stale, err)
	}
}
