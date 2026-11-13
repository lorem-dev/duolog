package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
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

func TestMainClearsPaidEnvironment(t *testing.T) {
	keys := []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"ANTHROPIC_PROFILE", "ANTHROPIC_FEDERATION_RULE_ID",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_BEARER_TOKEN_TEST",
		"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_FEDERATION_RULE_ID", "CODEX_API_KEY",
	}
	if os.Getenv("DUOLOG_TEST_CHECK_PAID_ENV") == "1" {
		for _, key := range keys {
			if _, ok := os.LookupEnv(key); ok {
				t.Errorf("inherited %s", key)
			}
		}
		return
	}
	for _, value := range []string{"test-credential", ""} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainClearsPaidEnvironment$")
		cmd.Env = append(os.Environ(), "DUOLOG_TEST_CHECK_PAID_ENV=1")
		for _, key := range keys {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
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

// packageTimeout is the least time this package's tests get. They run one
// after another and take about two minutes on a hosted runner, so a limit of
// two minutes for every package, as the release workflow sets, stops them on
// a slow runner. It stays under go test's own kill, a minute after the
// -timeout it was given, so a hang still ends in this binary's panic with
// every goroutine's stack.
const packageTimeout = 175 * time.Second

// raiseTimeout lifts a -test.timeout below packageTimeout to it. Go reads the
// flag when m.Run starts, so TestMain sets it before. No limit (0) stays.
func raiseTimeout() {
	flag.Parse()
	f := flag.Lookup("test.timeout")
	if f == nil {
		return
	}
	if given, err := time.ParseDuration(f.Value.String()); err == nil && given > 0 && given < packageTimeout {
		_ = f.Value.Set(packageTimeout.String())
	}
}

func TestMain(m *testing.M) {
	raiseTimeout()
	launchtest.ClearPaidEnvironment()
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	// A lead's session start refreshes the duolog skills under the home: no
	// test may reach the real one. A test that needs a home sets its own. An
	// agent folder under the temporary folder is one a parent test made for
	// this process, so it stays.
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if dir := os.Getenv(key); dir != "" && !strings.HasPrefix(dir, os.TempDir()) {
			if err := os.Unsetenv(key); err != nil {
				panic(err)
			}
		}
	}
	home, err := os.MkdirTemp("", "duolog-test-home-")
	if err != nil {
		panic(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(key, home); err != nil {
			panic(err)
		}
	}
	// A gate ask reads its reply every 10 ms instead of every half second; the
	// answers in these tests come at once.
	gate.AskPoll = 10 * time.Millisecond
	code := m.Run()
	_ = os.RemoveAll(home)
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
	d.watchTabLinger = 0
	testDepsFor.Store(name, d)
	t.Cleanup(func() { testDepsFor.Delete(name) })
	return d
}

func inTempDir(name string) bool {
	rel, err := filepath.Rel(pathutil.Resolved(os.TempDir()), pathutil.Resolved(name))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
