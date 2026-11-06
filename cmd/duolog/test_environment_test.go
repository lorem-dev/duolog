package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/gate"
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
	// A gate ask reads its reply every 10 ms instead of every half second; the
	// answers in these tests come at once.
	gate.AskPoll = 10 * time.Millisecond
	// A test that answers an ask in this process plays the person's terminal,
	// whatever agent runs the tests; a test of the walk starts duolog itself.
	agentAbove = func([]string) bool { return false }
	code := m.Run()
	_ = os.RemoveAll(accounts)
	if duologBuild.dir != "" {
		_ = os.RemoveAll(duologBuild.dir)
	}
	os.Exit(code)
}

// startEnv is the environment the test process started with, before any test
// moved HOME or the cache folders, so a build finds the Go build cache.
var startEnv = os.Environ()

var duologBuild struct {
	sync.Once
	dir, binary string
	err         error
}

// duologBinary builds cmd/duolog once per test process, alone in a folder of
// its own, and returns its path. Tests run it and never change it.
func duologBinary(t *testing.T) string {
	t.Helper()
	duologBuild.Do(func() {
		dir, err := os.MkdirTemp("", "duolog-test-bin-")
		if err != nil {
			duologBuild.err = err
			return
		}
		duologBuild.dir = dir
		binary := filepath.Join(dir, "duolog")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		_, source, _, _ := runtime.Caller(0)
		build := exec.Command("go", "build", "-o", binary, ".")
		build.Dir = filepath.Dir(source)
		build.Env = startEnv
		if output, err := build.CombinedOutput(); err != nil {
			duologBuild.err = fmt.Errorf("build: %s %w", output, err)
			return
		}
		duologBuild.binary = binary
	})
	if duologBuild.err != nil {
		t.Fatal(duologBuild.err)
	}
	return duologBuild.binary
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
