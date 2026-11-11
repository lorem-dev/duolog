package peer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func modelFile(r *rig, id string) string {
	return filepath.Join(r.reg.Dir, "coauthor-"+id+".model")
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// TestPeerCloseRemovesModelFile: a closed peer's model file goes after the
// registry drops it; an open peer keeps its own.
func TestPeerCloseRemovesModelFile(t *testing.T) {
	r := newRig(t, baseScript())
	r.git()
	r.lead("claude", mode.Edits)
	p := r.open("implement")
	if !exists(t, modelFile(r, p.ID)) {
		t.Fatal("open wrote no model file")
	}
	if err := Close(r.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if exists(t, modelFile(r, p.ID)) {
		t.Fatal("model file of a closed peer remains")
	}
}

// TestPeerOpenSurvivesSweepFailure: a model file the sweep cannot remove does
// not fail the open.
func TestPeerOpenSurvivesSweepFailure(t *testing.T) {
	r := newRig(t, baseScript())
	r.git()
	r.lead("claude", mode.Edits)
	live := r.open("implement")
	dir := r.reg.Dir
	stuck := filepath.Join(dir, "coauthor-peer-zzzzzz.model")
	if err := os.WriteFile(stuck, []byte("m"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &env{dir: dir, reg: r.reg}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if err := e.sweepModels(); err == nil {
		t.Skip("state folder stays writable here (running as root?)")
	}
	// The folder stays read-only, so the sweep fails again inside Open.
	again := r.open("implement")
	if again.ID != live.ID {
		t.Fatalf("open returned %s, want the live peer %s", again.ID, live.ID)
	}
}

// TestPeerOpenSweepsGoneModelFiles: Open removes only coauthor-peer-<id>.model
// files whose peer is not in the registry.
func TestPeerOpenSweepsGoneModelFiles(t *testing.T) {
	r := newRig(t, baseScript())
	r.git()
	r.lead("claude", mode.Edits)
	live := r.open("implement")
	dir := r.reg.Dir
	write := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("m"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	gone := write("coauthor-peer-zzzzzz.model")
	other := write("coauthor-peer-zzzzzz.model.bak")
	odd := write("coauthor-peer-a.b.model")
	nested := write("coauthor-peer-.model")
	target := filepath.Join(t.TempDir(), "target.model")
	if err := os.WriteFile(target, []byte("m"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "coauthor-peer-linked.model")
	if err := os.Symlink(target, link); err != nil {
		// Windows without Developer Mode or admin rights cannot make one.
		t.Skipf("symlinks unavailable: %v", err)
	}
	folder := filepath.Join(dir, "coauthor-peer-folder.model")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	r.open("implement") // reuses the live peer; the sweep runs first
	if exists(t, gone) {
		t.Error("model file of a peer not in the registry remains")
	}
	for name, path := range map[string]string{"live": modelFile(r, live.ID), "other name": other, "odd id": odd, "empty id": nested, "symlink": link, "symlink target": target, "directory": folder} {
		if !exists(t, path) {
			t.Errorf("%s was removed", name)
		}
	}
}
