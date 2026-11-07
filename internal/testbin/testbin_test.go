package testbin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// TestPlacedFakeSurvivesOthersComingAndGoing starts fakes while other fakes
// are placed and removed, as parallel test packages do through Install and
// t.TempDir cleanup. With a hard link to one shared file, macOS killed about
// one start in 1000 here with SIGKILL, so a run failed far more often than not.
func TestPlacedFakeSurvivesOthersComingAndGoing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows copies each fake, so no start shares a file")
	}
	if err := buildFake(); err != nil {
		t.Fatal(err)
	}
	scenario, err := json.Marshal(Scenario{Stdout: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	const workers, starts = 32, 30
	errs := make(chan error, workers*starts)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i := range starts {
				errs <- startPlaced(filepath.Join(root, fmt.Sprintf("%d-%d", w, i)), scenario)
			}
		})
	}
	wg.Wait()
	close(errs)
	failed := 0
	for err := range errs {
		if err != nil {
			if failed++; failed <= 3 {
				t.Error(err)
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d of %d starts failed", failed, workers*starts)
	}
}

// startPlaced places a fake in dir, runs it once and removes dir.
func startPlaced(dir string, scenario []byte) error {
	defer os.RemoveAll(dir)
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	dest := filepath.Join(dir, "fake")
	if err := place(dest); err != nil {
		return err
	}
	if err := os.WriteFile(dest+".json", scenario, 0600); err != nil {
		return err
	}
	out, err := exec.Command(dest).Output()
	if err != nil {
		return fmt.Errorf("%s: %w", dest, err)
	}
	if string(out) != "ok" {
		return fmt.Errorf("%s printed %q", dest, out)
	}
	return nil
}
