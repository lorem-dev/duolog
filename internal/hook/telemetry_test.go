package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/telemetry"
)

// Hook tests never write the person's telemetry: collection is off and the
// roots sit in a temporary folder unless a test turns it on with
// telemetryOn.
func init() {
	dir, err := os.MkdirTemp("", "duolog-hook-telemetry-")
	if err != nil {
		panic(err)
	}
	for key, value := range map[string]string{"DUOLOG_TELEMETRY": "off", "XDG_STATE_HOME": filepath.Join(dir, "state"), "LOCALAPPDATA": filepath.Join(dir, "local")} {
		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	}
}

// telemetryOn turns collection on with the roots inside the test's home and
// returns the telemetry root.
func telemetryOn(t *testing.T) string {
	t.Helper()
	home := os.Getenv("HOME")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("DUOLOG_TELEMETRY", "")
	root, err := telemetry.ResolveRoot(telemetryLocation())
	if err != nil || !strings.HasPrefix(root, home) {
		t.Fatalf("telemetry root %s outside the temporary home: %v", root, err)
	}
	return root
}

func startContext(t *testing.T, e Env) string {
	t.Helper()
	o := SessionStart(context.Background(), e)
	if len(o.Stdout) == 0 {
		return ""
	}
	return content(t, o, "SessionStart")
}

func TestTelemetryFirstSessionNotice(t *testing.T) {
	t.Run("once per collecting user", func(t *testing.T) {
		e, _, _ := setup(t, "claude")
		root := telemetryOn(t)
		if got := startContext(t, e); !strings.Contains(got, noticeLine) {
			t.Fatalf("first session has no notice:\n%s", got)
		}
		if got := startContext(t, e); strings.Contains(got, noticeLine) {
			t.Fatalf("the notice repeats:\n%s", got)
		}
		info, err := os.Lstat(filepath.Join(root, noticeName))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("no notice marker: %v", err)
		}
		// The user's session with the other agent does not repeat it.
		other := e
		other.Agent = "codex"
		if got := telemetryStart(context.Background(), other); strings.Contains(got, noticeLine) {
			t.Fatalf("the notice repeats for the user's other session:\n%s", got)
		}
	})

	t.Run("concurrent starts say it once", func(t *testing.T) {
		e, _, _ := setup(t, "claude")
		telemetryOn(t)
		var wg sync.WaitGroup
		var mu sync.Mutex
		said := 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if strings.Contains(telemetryStart(context.Background(), e), noticeLine) {
					mu.Lock()
					said++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if said != 1 {
			t.Fatalf("notice said %d times", said)
		}
	})

	t.Run("off suppresses it", func(t *testing.T) {
		for _, how := range []string{"env", "user config"} {
			t.Run(how, func(t *testing.T) {
				e, _, _ := setup(t, "claude")
				root := telemetryOn(t)
				if how == "env" {
					t.Setenv("DUOLOG_TELEMETRY", "off")
				} else {
					dir := filepath.Join(userConfigDir(t), "duolog")
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					write(t, filepath.Join(dir, "config.yaml"), "telemetry:\n  enabled: false\n")
				}
				if got := startContext(t, e); strings.Contains(got, noticeLine) || strings.Contains(got, "telemetry:") {
					t.Fatalf("off says %q", got)
				}
				if _, err := os.Lstat(root); err == nil {
					t.Fatal("off created the root")
				}
			})
		}
	})

	t.Run("off and on again does not repeat", func(t *testing.T) {
		e, _, _ := setup(t, "claude")
		telemetryOn(t)
		if got := startContext(t, e); !strings.Contains(got, noticeLine) {
			t.Fatal("no first notice")
		}
		t.Setenv("DUOLOG_TELEMETRY", "off")
		startContext(t, e)
		t.Setenv("DUOLOG_TELEMETRY", "")
		if got := startContext(t, e); strings.Contains(got, noticeLine) {
			t.Fatal("the notice repeats after off and on")
		}
	})

	t.Run("unsafe root never claims collecting", func(t *testing.T) {
		e, _, _ := setup(t, "claude")
		root := telemetryOn(t)
		// The root resolves into a git work tree, so it is refused.
		if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		got := startContext(t, e)
		if strings.Contains(got, noticeLine) || strings.Contains(got, "local only") || !strings.Contains(got, "telemetry: unhealthy") {
			t.Fatalf("unsafe root says %q", got)
		}
		if strings.Contains(got, "git") || strings.Contains(got, root) {
			t.Fatalf("hook context names the refusal: %q", got)
		}
		if _, err := os.Lstat(filepath.Join(root, noticeName)); err == nil {
			t.Fatal("unsafe root got a notice marker")
		}
	})

	t.Run("workers never see it", func(t *testing.T) {
		e, _, _ := setup(t, "claude")
		root := telemetryOn(t)
		e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
		if got := telemetryStart(context.Background(), e); got != "" {
			t.Fatalf("worker got %q", got)
		}
		if _, err := os.Lstat(root); err == nil {
			t.Fatal("a worker created the root")
		}
	})
}

func userConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// A lead's session start prunes the delegation folders and telemetry while
// collection is off; a worker's prunes neither.
func TestTelemetrySessionMaintenance(t *testing.T) {
	for _, workerRun := range []bool{false, true} {
		e, _, _ := setup(t, "claude")
		root := telemetryOn(t)
		s, err := telemetry.Open(telemetry.Options{Location: telemetryLocation(), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Append(telemetry.Reset{Agent: "claude", Reason: "manual"}); err != nil {
			t.Fatal(err)
		}
		s.Close()
		day := time.Now().UTC().AddDate(0, 0, -40).Format(time.DateOnly)
		expired := filepath.Join(root, "v1", "_account", day+".ndjson")
		write(t, expired, "{}\n")
		stale := time.Now().Add(-7 * time.Hour)
		if err := os.Chtimes(filepath.Join(root, "prune.stamp"), stale, stale); err != nil {
			t.Fatal(err)
		}
		old := filepath.Join(e.Root, ".duolog", "tasks", "task-old")
		if err := os.MkdirAll(old, 0755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-store.KeepFor - time.Hour)
		if err := os.Chtimes(old, at, at); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DUOLOG_TELEMETRY", "off")
		if workerRun {
			e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
		}
		SessionStart(context.Background(), e)
		if _, err := os.Stat(old); (err == nil) != workerRun {
			t.Fatalf("worker=%v: task folder kept=%v", workerRun, err == nil)
		}
		if _, err := os.Stat(expired); (err == nil) != workerRun {
			t.Fatalf("worker=%v: expired telemetry kept=%v", workerRun, err == nil)
		}
	}
}
