package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

func TestInitialAskLocksRespectDeadline(t *testing.T) {
	// Kills unbounded initial registry reads and bus sends before the reply poll.
	for _, name := range []string{"sessions.lock", "bus.lock"} {
		t.Run(name, func(t *testing.T) {
			b, _, dir := askSetup(t)
			l := flock.New(filepath.Join(dir, name))
			if err := l.Lock(); err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			released := make(chan struct{})
			go func() { time.Sleep(200 * time.Millisecond); _ = l.Unlock(); close(released) }()
			start := time.Now()
			d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: "make lint"}, 30*time.Millisecond, false)
			elapsed := time.Since(start)
			<-released
			if d.Kind != Deny || elapsed > 150*time.Millisecond {
				t.Fatalf("decision %+v took %s", d, elapsed)
			}
		})
	}
}

func TestPermissionRecordsHideCredentials(t *testing.T) {
	// Kills persistence of raw command credentials, including deny reasons.
	for _, command := range []string{
		"curl -H 'Authorization: Bearer example-value' https://example.invalid",
		"curl -u sample:example-value https://example.invalid",
		"curl --user sample:example-value https://example.invalid",
		"curl -usample:example-value https://example.invalid",
		`curl -H"X-Credential: example-value" https://example.invalid`,
		"curl https://sample:example-value@example.invalid",
		"ACCESS_TOKEN=example-value command",
	} {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			a := Action{Kind: "exec", Target: command}
			for _, log := range []func(*store.Store, Action, Decision) error{Log, LogRequest} {
				if err := log(&store.Store{Dir: dir}, a, Decision{Kind: Deny, Reason: a.Target}); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"decisions.ndjson", "requests.ndjson"} {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "example-value") {
					t.Fatalf("credential in %s", name)
				}
			}
		})
	}
}

func TestAskSummariesHideCredentials(t *testing.T) {
	// Kills leaking credential-bearing commands through ask and expiry bus summaries.
	b, _, dir := askSetup(t)
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: "curl -u sample:example-value https://example.invalid"}, askSendWait, false)
	messages := askMessages(t, dir)
	if len(messages) == 0 {
		t.Fatal("no ask")
	}
	for _, m := range messages {
		if strings.Contains(m.S, "example-value") {
			t.Fatal("credential in bus summary")
		}
	}
}

func TestDecisionLogLockIsBounded(t *testing.T) {
	// Kills an indefinite decision-log lock wait before the hook renders deny.
	dir := t.TempDir()
	l := flock.New(filepath.Join(dir, "decisions.lock"))
	if err := l.Lock(); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() {
		done <- Log(&store.Store{Dir: dir}, Action{Kind: "read", Target: ".env"}, Decision{Kind: Deny})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("held lock accepted")
		}
	case <-time.After(1500 * time.Millisecond):
		_ = l.Unlock()
		<-done
		t.Fatal("decision log waited indefinitely")
	}
}

func TestLinkedWorkerInheritsMainDenies(t *testing.T) {
	// Kills importing only the linked checkout's settings and losing local denies.
	p := testPolicy(t)
	main, work := t.TempDir(), t.TempDir()
	if out, err := exec.CommandContext(context.Background(), "git", "init", "-q", main).CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	gitdir := filepath.Join(main, ".git", "worktrees", "worker")
	for path, data := range map[string]string{
		filepath.Join(gitdir, "commondir"):                    "../..\n",
		filepath.Join(gitdir, "HEAD"):                         "ref: refs/heads/worker\n",
		filepath.Join(work, ".git"):                           "gitdir: " + gitdir + "\n",
		filepath.Join(main, ".claude", "settings.local.json"): `{"permissions":{"deny":["mcp__tracker__create_issue"]}}`,
		filepath.Join(work, ".claude", "settings.local.json"): `{"permissions":{"deny":["Bash(rm:*)"]}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rules, err := ImportLead(work, "claude")
	if err != nil {
		t.Fatal(err)
	}
	check(t, p, rules, mode.Full, Action{Kind: "mcp", Target: "tracker/create_issue", Work: work}, Deny)
	check(t, p, rules, mode.Full, Action{Kind: "exec", Target: "rm file", Work: work}, Deny)
}

func TestLeadModeLockIsBounded(t *testing.T) {
	// Kills unbounded registry locks while resolving the gate's live lead mode.
	_, reg, dir := askSetup(t)
	lock := flock.New(filepath.Join(dir, "sessions.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	done := make(chan error, 1)
	go func() { _, _, err := LeadMode(reg, "first-123456", time.Now()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("held registry returned a mode")
		}
	case <-time.After(1500 * time.Millisecond):
		_ = lock.Unlock()
		<-done
		t.Fatal("lead mode waited indefinitely")
	}
}
