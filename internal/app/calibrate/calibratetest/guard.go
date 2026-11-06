// Package calibratetest gives tests of calibrate and of the agent adapters a
// fake calibration.TreeGuard and TreeGuardContract, the cases every guard
// meets. It does not import calibrate, so calibrate's own tests may use it.
// Only tests import it.
package calibratetest

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// TreeGuard has the methods of calibration.TreeGuard.
type TreeGuard interface {
	Prepare(dir string, environ []string) error
	Check(dir string, environ []string) error
}

// Fake is a TreeGuard that refuses every tree it did not prepare. It records
// each call.
type Fake struct {
	mu sync.Mutex
	// Prepared and Checked are the folders of the calls, in order.
	Prepared, Checked []string
	// Environ is the environment of the last call.
	Environ []string
}

// Refusal is what Fake.Check says about a tree it did not prepare.
const Refusal = "the fake guard was not prepared"

// Prepare remembers dir.
func (f *Fake) Prepare(dir string, environ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Prepared = append(f.Prepared, dir)
	f.Environ = environ
	return nil
}

// Check passes for a folder Prepare saw and refuses any other.
func (f *Fake) Check(dir string, environ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Checked = append(f.Checked, dir)
	f.Environ = environ
	for _, p := range f.Prepared {
		if p == dir {
			return nil
		}
	}
	return fmt.Errorf("%s in %s", Refusal, dir)
}

// environ is the environment of the contract's calls: the test's own, which
// newGuard has pointed at temporary homes.
func environ() []string { return os.Environ() }

// TreeGuardContract runs the cases every guard meets. newGuard returns the
// guard, grant and refusal: grant does for dir what only the person can (a
// Codex trust; nothing for an agent that needs none), and refusal is a text
// the guard's Check error holds for a tree that lacks what it needs, besides
// the tree's folder.
func TreeGuardContract(t *testing.T, newGuard func(t *testing.T) (guard TreeGuard, grant func(dir string), refusal string)) {
	t.Helper()
	t.Run("prepare then check passes", func(t *testing.T) {
		guard, grant, _ := newGuard(t)
		dir := t.TempDir()
		if err := guard.Prepare(dir, environ()); err != nil {
			t.Fatal(err)
		}
		grant(dir)
		if err := guard.Check(dir, environ()); err != nil {
			t.Fatalf("Check after Prepare on an empty tree: %v", err)
		}
	})
	t.Run("check refuses a tree without it", func(t *testing.T) {
		guard, _, refusal := newGuard(t)
		dir := t.TempDir()
		err := guard.Check(dir, environ())
		if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), refusal) {
			t.Fatalf("Check on a bare tree = %v, want a refusal naming %s and %q", err, dir, refusal)
		}
	})
}
