package rex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

const splitReply = `{"anchor_id":"block:a","block_ids":["block:new"],"split_ids":["split:s"],"revision":4}`

func calls(t *testing.T, bin string) [][]string {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var all [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var words []string
		if err := json.Unmarshal([]byte(line), &words); err != nil {
			t.Fatal(err)
		}
		all = append(all, words)
	}
	return all
}

func install(t *testing.T, s testbin.Scenario) Client {
	t.Helper()
	bin := testbin.Install(t, "rex", s)
	return Client{Bin: bin, Session: "sess"}
}

func has(words []string, want string) bool {
	for _, w := range words {
		if w == want {
			return true
		}
	}
	return false
}

func TestSplitDoesNotStealFocus(t *testing.T) {
	c := install(t, testbin.Scenario{Responses: map[string]testbin.Scenario{"split": {Stdout: splitReply}}})
	c.Anchor, c.Dir, c.Label = "block:lead", "/work", "codex-plan"
	id, err := c.Split(context.Background(), []string{"codex", "-m", "x"})
	if err != nil || id != "block:new" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	got := calls(t, c.Bin)
	if len(got) != 1 || !has(got[0], "--focus=false") || !has(got[0], "--json") || !has(got[0], "--shell=none") {
		t.Fatalf("calls=%v", got)
	}
	tail := strings.Join(got[0], " ")
	for _, want := range []string{"-s sess", "-b block:lead", "-c /work", "--label codex-plan", "-- codex -m x"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("missing %q in %s", want, tail)
		}
	}
	// The program and its arguments follow the separator, so none can be an option.
	if strings.Index(tail, "--focus=false") > strings.Index(tail, " -- ") {
		t.Fatal(tail)
	}
}

func TestSplitRejectsReplyWithoutBlock(t *testing.T) {
	for _, reply := range []string{`{}`, `{"block_ids":[]}`, `not json`} {
		c := install(t, testbin.Scenario{Stdout: reply})
		if _, err := c.Split(context.Background(), []string{"codex"}); err == nil {
			t.Fatalf("reply %q accepted", reply)
		}
	}
}

func TestSendAndKeyUseTheBlock(t *testing.T) {
	c := install(t, testbin.Scenario{})
	if err := c.Send(context.Background(), "block:p", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendKey(context.Background(), "block:p", "ctrl+u"); err != nil {
		t.Fatal(err)
	}
	got := calls(t, c.Bin)
	if strings.Join(got[0], " ") != "send -s sess -b block:p hello" || strings.Join(got[1], " ") != "send-key -s sess -b block:p ctrl+u" {
		t.Fatalf("calls=%v", got)
	}
}

func TestAliveCapturesLimitedLines(t *testing.T) {
	var screen strings.Builder
	for i := 0; i < 100; i++ {
		screen.WriteString("line\n\n")
	}
	c := install(t, testbin.Scenario{Responses: map[string]testbin.Scenario{
		"block inspect": {Stdout: `{"block":{}}`},
		"wait":          {Exit: 1, Stderr: "timed out"},
		"capture":       {Stdout: screen.String()},
	}})
	alive, err := c.Alive(context.Background(), "block:p", 5)
	if err != nil || !alive {
		t.Fatalf("alive=%v err=%v", alive, err)
	}
	out, err := c.Capture(context.Background(), "block:p", 5)
	if err != nil || strings.Count(string(out), "\n") != 4 || strings.Contains(string(out), "\n\n") {
		t.Fatalf("capture is not cut to 5 non-blank lines: %q err=%v", out, err)
	}
}

func TestCaptureDropsControlCharacters(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: "ok\x1b[31m red\x07\n"})
	out, err := c.Capture(context.Background(), "b", 20)
	if err != nil || string(out) != "ok[31m red" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestProbeTellsClosedFromExitedFromRunning(t *testing.T) {
	cases := []struct {
		name string
		s    map[string]testbin.Scenario
		want Presence
	}{
		{"running", map[string]testbin.Scenario{"block inspect": {Stdout: "{}"}, "wait": {Exit: 1, Stderr: "timed out"}}, Running},
		{"exited", map[string]testbin.Scenario{"block inspect": {Stdout: "{}"}, "wait": {Exit: 3, Stdout: `{"exit_code": 3, "error": "exit status 3"}`}}, Exited},
		{"exited zero", map[string]testbin.Scenario{"block inspect": {Stdout: "{}"}, "wait": {Stdout: `{"exit_code": 0}`}}, Exited},
		{"closed", map[string]testbin.Scenario{"block inspect": {Exit: 3, Stderr: `block target "block:p" not found`}}, Gone},
		{"closed during wait", map[string]testbin.Scenario{"block inspect": {Stdout: "{}"}, "wait": {Exit: 3, Stderr: `block, window, or session target "block:p" not found`}}, Gone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := install(t, testbin.Scenario{Responses: tc.s})
			got, err := c.Probe(context.Background(), "block:p")
			if err != nil || got != tc.want {
				t.Fatalf("got %v err=%v", got, err)
			}
			alive, err := c.Alive(context.Background(), "block:p", 5)
			if err != nil || alive != (tc.want == Running) {
				t.Fatalf("alive=%v err=%v", alive, err)
			}
		})
	}
}

func TestSocketUnreachableIsOneLine(t *testing.T) {
	c := install(t, testbin.Scenario{Exit: 4, Stderr: "connecting to Rex server: failed to WebSocket dial\nmore\n"})
	for name, call := range map[string]func() error{
		"send":  func() error { return c.Send(context.Background(), "b", "x") },
		"split": func() error { _, err := c.Split(context.Background(), []string{"x"}); return err },
		"probe": func() error { _, err := c.Probe(context.Background(), "b"); return err },
		"close": func() error { return c.Close(context.Background(), "b") },
	} {
		err := call()
		if !errors.Is(err, ErrSocket) || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "sandbox") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestCloseAlreadyGoneIsQuiet(t *testing.T) {
	c := install(t, testbin.Scenario{Exit: 3, Stderr: `block target "block:p" not found`})
	if err := c.Close(context.Background(), "block:p"); err != nil {
		t.Fatal(err)
	}
	other := install(t, testbin.Scenario{Exit: 1, Stderr: "boom"})
	if err := other.Close(context.Background(), "block:p"); err == nil {
		t.Fatal("other failures are errors")
	}
}

func TestClosePassesBlockAfterSession(t *testing.T) {
	c := install(t, testbin.Scenario{})
	if err := c.Close(context.Background(), "block:p"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls(t, c.Bin)[0], " "); got != "block close -s sess block:p" {
		t.Fatal(got)
	}
}

func TestStdinIsEmptyAndRunIsBounded(t *testing.T) {
	old := CommandTimeout
	CommandTimeout = 100 * time.Millisecond
	t.Cleanup(func() { CommandTimeout = old })
	c := install(t, testbin.Scenario{SleepMS: 3000})
	start := time.Now()
	if err := c.Send(context.Background(), "b", "x"); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("hung call returned %v after %v", err, time.Since(start))
	}
}

func TestRexBinaryFromAppBundle(t *testing.T) {
	name := "rex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	apps := t.TempDir()
	bundle := filepath.Join(apps, "Rex Test.app", "Contents", "Helpers", "Server", "Contents", "MacOS")
	if err := os.MkdirAll(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	inBundle := filepath.Join(bundle, name)
	envDir := t.TempDir()
	inEnv := filepath.Join(envDir, name)
	for _, path := range []string{inBundle, inEnv} {
		if err := os.WriteFile(path, []byte("x"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	glob := filepath.Join(apps, "Rex*.app", "Contents", "Helpers", "*", "Contents", "MacOS", name)
	if got, ok := Locate("", []string{glob}); !ok || got != inBundle {
		t.Fatalf("bundle: %q %v", got, ok)
	}
	if got, ok := Locate(envDir, []string{glob}); !ok || got != inEnv {
		t.Fatalf("REX_BIN_DIR must win over the bundle: %q %v", got, ok)
	}
	onPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(onPath, name), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath)
	if got, ok := Locate(envDir, []string{glob}); !ok || got != filepath.Join(onPath, name) {
		t.Fatalf("PATH must win: %q %v", got, ok)
	}
	t.Setenv("PATH", empty)
	if _, ok := Locate("", []string{filepath.Join(empty, "none*")}); ok {
		t.Fatal("found a rex that is not there")
	}
}

func TestDetectNeedsSessionAndBinary(t *testing.T) {
	bin := testbin.Install(t, "rex", testbin.Scenario{})
	t.Setenv("REX_SESSION", "")
	if _, ok := Detect(); ok {
		t.Fatal("outside Rex")
	}
	t.Setenv("REX_SESSION", "work")
	t.Setenv("REX_BLOCK", "block:lead")
	c, ok := Detect()
	if !ok || c.Session != "work" || c.Anchor != "block:lead" || filepath.Base(c.Bin) != filepath.Base(bin) {
		t.Fatalf("%+v %v", c, ok)
	}
}
