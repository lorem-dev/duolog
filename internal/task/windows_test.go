package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsReadPathsAndCRLF(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "file.txt"), []byte("first\r\nsecond\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(root, "task.md")
	if err := os.WriteFile(task, []byte("## Goal\r\nx\r\n## Read\r\n- `src\\file.txt`:2-2\r\n## Write\r\n## Limits\r\n## Check\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	refs, err := References(task)
	if err != nil || len(refs) != 1 || refs[0].Path != "src/file.txt" {
		t.Fatal(refs, err)
	}
	got, err := refs[0].Text(root)
	if err != nil || string(got) != "second\r\n" {
		t.Fatal(string(got), err)
	}
	for _, outside := range []string{`..\secret`, `C:\secret`, `D:relative`} {
		if _, err := Resolve(root, outside); err == nil {
			t.Fatal("accepted", outside)
		}
	}
}
