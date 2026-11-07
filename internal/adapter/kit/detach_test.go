package kit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// StartDetached starts the process in its folder without waiting for it, and
// its output goes to the end of the log: what the log held stays.
func TestStartDetachedAppendsToTheLog(t *testing.T) {
	bin := testbin.Install(t, "detached", testbin.Scenario{Stdout: "the run\n"})
	dir := t.TempDir()
	log := filepath.Join(dir, "run.log")
	if err := os.WriteFile(log, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := StartDetached([]string{bin, "go"}, dir, log); err != nil {
		t.Fatal(err)
	}
	if data := waitForLog(t, log, "the run"); !strings.HasPrefix(data, "before\n") {
		t.Fatalf("the log lost what it held: %q", data)
	}
}

// A queue run starts before its log exists: StartDetached creates it.
func TestStartDetachedCreatesTheLog(t *testing.T) {
	bin := testbin.Install(t, "detached", testbin.Scenario{Stdout: "the run\n"})
	dir := t.TempDir()
	log := filepath.Join(dir, "run.log")
	if err := StartDetached([]string{bin, "go"}, dir, log); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, log, "the run")
}

// waitForLog waits up to ten seconds for the file at log to hold want, and
// returns what it holds.
func waitForLog(t *testing.T, log, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		if strings.Contains(string(data), want) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no output from the detached process: %q", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
