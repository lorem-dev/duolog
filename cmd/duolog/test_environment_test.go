package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/pathutil"
)

func TestMainClearsWorkerEnvironment(t *testing.T) {
	if os.Getenv("DUOLOG_TEST_CHECK_ENV") == "1" {
		for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
			if os.Getenv(key) != "" {
				t.Errorf("inherited %s", key)
			}
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainClearsWorkerEnvironment$")
	cmd.Env = append(os.Environ(), "DUOLOG_TEST_CHECK_ENV=1", "DUOLOG_DEPTH=2", "DUOLOG_LEAD=other", "DUOLOG_ROLE=implement")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
}

// A doctor test that would find the machine's own rex stops instead; one that
// puts a fake rex first on PATH finds the fake.
func TestDoctorTestsRefuseMachineRex(t *testing.T) {
	fake := fakeRexFirst(t)
	if bin, ok := locateRex(); !ok || !samePath(bin, fake) {
		t.Fatalf("doctor found %q (%v), want the fake %s", bin, ok, fake)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a rex outside the temporary folder did not stop the test")
		}
	}()
	testRex(func() (string, bool) {
		return "/Applications/Rex.app/Contents/Helpers/rex.app/Contents/MacOS/rex", true
	})()
}

func TestMain(m *testing.M) {
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	// No test runs this machine's rex: doctor tests put a fake rex first on
	// PATH, and doctor finding any other rex stops the run before it starts.
	locateRex = testRex(locateRex)
	// Delegations admit their runs through a gate whose quota reads start no
	// binary and whose account state is temporary, never the user's.
	accounts, err := os.MkdirTemp("", "duolog-accounts-")
	if err != nil {
		panic(err)
	}
	// One state per test folder: a peer's open, send and close share it.
	delegate.NewGate = paidrunstest.ByWorkingDir(accounts)
	code := m.Run()
	_ = os.RemoveAll(accounts)
	os.Exit(code)
}

// testRex wraps doctor's rex lookup for tests: a rex outside the temporary
// folder, such as the one in an installed Rex app, panics before it runs.
func testRex(locate func() (string, bool)) func() (string, bool) {
	return func() (string, bool) {
		bin, ok := locate()
		if ok && !inTempDir(bin) {
			panic("doctor found " + bin + ", not a fake: put a fake rex first on PATH with fakeRexFirst")
		}
		return bin, ok
	}
}

func inTempDir(name string) bool {
	rel, err := filepath.Rel(pathutil.Resolved(os.TempDir()), pathutil.Resolved(name))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
