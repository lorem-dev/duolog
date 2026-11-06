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

	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/wiring/wiringtest"
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
	if bin, ok := testDeps(t).LocateRex(); !ok || !samePath(bin, fake) {
		t.Fatalf("doctor found %q (%v), want the fake %s", bin, ok, fake)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a rex outside the temporary folder did not stop the test")
		}
	}()
	wiringtest.GuardRex(func() (string, bool) {
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
	// A gate ask reads its reply every 10 ms instead of every half second; the
	// answers in these tests come at once.
	gate.AskPoll = 10 * time.Millisecond
	code := m.Run()
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

// testDepsFor holds the dependencies of the commands for each running test.
var testDepsFor sync.Map

// testDeps returns the dependencies of the commands for t: wiringtest.Cmd's,
// the same each time, so that a helper changes a field and the command the
// test builds afterwards reads it. A subtest starts from its parent's.
func testDeps(t *testing.T) *cmdDeps {
	t.Helper()
	name := t.Name()
	for n := name; n != ""; {
		if d, ok := testDepsFor.Load(n); ok {
			return d.(*cmdDeps)
		}
		i := strings.LastIndex(n, "/")
		if i < 0 {
			break
		}
		n = n[:i]
	}
	d := newCmdDeps(wiringtest.Cmd(t))
	testDepsFor.Store(name, d)
	t.Cleanup(func() { testDepsFor.Delete(name) })
	return d
}

func inTempDir(name string) bool {
	rel, err := filepath.Rel(pathutil.Resolved(os.TempDir()), pathutil.Resolved(name))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
