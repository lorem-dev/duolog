package hook

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotDir reads every file under dir, by relative path.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			files[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		files[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestLoadLineHasNoSideEffects(t *testing.T) {
	e, b := registered(t, "claude")
	tellLead(t, b, e) // an unread bus message a delivery would consume
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	var queued []string
	e.QueueRun = func(_ context.Context, leadID string) { queued = append(queued, leadID) }
	dir := filepath.Join(e.Root, ".duolog")
	before := snapshotDir(t, dir)

	got, err := LoadLine(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantLoad {
		t.Fatalf("LoadLine = %q, want %q", got, wantLoad)
	}
	if seen != id(e) {
		t.Fatalf("Load asked for lead %q, want %q", seen, id(e))
	}
	if len(queued) != 0 {
		t.Fatalf("LoadLine started the queue for %v", queued)
	}
	after := snapshotDir(t, dir)
	if len(after) != len(before) {
		t.Fatalf("files changed: before %d, after %d", len(before), len(after))
	}
	for name, data := range before {
		if after[name] != data {
			t.Fatalf("%s changed", name)
		}
	}
}

func TestLoadLineIsEmptyForPeersAndWorkers(t *testing.T) {
	e, _ := registered(t, "claude")
	var calls int
	var seen string
	e = withLoad(e, &calls, &seen)
	e.Environ = append(os.Environ(), "DUOLOG_PEER=peer-1")
	if got, err := LoadLine(context.Background(), e); err != nil || got != "" || calls != 0 {
		t.Fatalf("peer: %q %v after %d calls", got, err, calls)
	}
	e.Environ = append(os.Environ(), "DUOLOG_DEPTH=1")
	if got, err := LoadLine(context.Background(), e); err != nil || got != "" || calls != 0 {
		t.Fatalf("worker: %q %v after %d calls", got, err, calls)
	}
}

func TestLoadLineNeedsASessionId(t *testing.T) {
	e, _ := registered(t, "claude")
	e.Load = withLoad(e, new(int), new(string)).Load
	e.Input = nil
	if _, err := LoadLine(context.Background(), e); err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("err = %v", err)
	}
}
