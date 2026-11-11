package hook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestSubmoduleAskOwnershipAndWake(t *testing.T) {
	e, _, _ := setup(t, "claude")
	f := submodule.New(t)
	for _, root := range []string{f.Superproject, f.Submodule} {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, ".duolog", "config.yaml"), "{}\n")
	}
	e.Root, e.WorkRoot = f.Submodule, f.Linked
	reg, err := session.Open(project.LeadHome(f.Linked))
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(reg.Dir, defaults(t).Wire)
	if err != nil {
		t.Fatal(err)
	}
	SessionStart(context.Background(), e)
	address := session.Address(lead(t, reg, e))
	send(t, b, address, "submodule summary")
	if out := Prompt(context.Background(), e); !strings.Contains(string(out.Stdout), "submodule summary") {
		t.Fatal(string(out.Stdout))
	}
	var stderr bytes.Buffer
	e.Stderr = &stderr
	send(t, b, address, "wake submodule")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if Wake(ctx, e) != 2 || !strings.Contains(stderr.String(), "wake submodule") {
		t.Fatal(stderr.String())
	}
	checkSubmoduleStop(t, e, reg, b, address)
	if err := reg.SetMode(id(e), mode.Plan); err != nil {
		t.Fatal(err)
	}
	Observe(context.Background(), change(t, e, "permission_mode", "bypassPermissions"))
	if _, m, err := reg.EffectiveMode(id(e)); err != nil || m != mode.Plan {
		t.Fatal(m, err)
	}
	SessionEnd(context.Background(), e)
	if _, alive, err := reg.Lead(id(e)); err != nil || alive {
		t.Fatal("stale lead", alive, err)
	}
}

func checkSubmoduleStop(t *testing.T, e Env, reg *session.Registry, b *bus.Bus, address string) {
	t.Helper()
	noticeWait(t, 2*time.Second)
	entry := session.Entry{ID: "peer-submodule", Lead: id(e), Agent: "codex", Role: "implement", Block: "block:fake", WorkRoot: e.WorkRoot, Task: "task.md"}
	if err := reg.Record(entry); err != nil {
		t.Fatal(err)
	}
	pane := withAgent(Env{Root: e.Root, WorkRoot: e.WorkRoot, Input: stopInput(t, "", "finished submodule"), Environ: []string{"DUOLOG_DEPTH=1", "DUOLOG_LEAD=" + id(e), "DUOLOG_PEER=" + entry.ID}}, "codex")
	countTurn(context.Background(), pane)
	Stop(context.Background(), pane)
	if mail := allMail(t, b, address); len(mail) < 2 {
		t.Fatal(mail)
	}
	e.Peers = func(context.Context, string) []string { return []string{"peer-submodule working"} }
	if out := Stop(context.Background(), e); !strings.Contains(string(out.Stdout), "peer-submodule") {
		t.Fatal(string(out.Stdout))
	}
}

// A lead registered from a submodule checkout keeps that checkout as its
// root in the shared lead home; a later prompt or /clear from another checkout
// does not move it, and no lead state is written in the submodule.
func TestHookSubmoduleRetainsLeadCheckout(t *testing.T) {
	setup(t, "claude")
	f := submodule.New(t)
	for _, root := range []string{f.Superproject, f.Submodule} {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, ".duolog", "config.yaml"), "{}\n")
	}
	e := withAgent(Env{Root: f.Submodule, WorkRoot: f.Linked,
		Input: []byte(`{"session_id":"hook-lead","permission_mode":"default"}`), Environ: []string{"CLAUDE_CODE_ENTRYPOINT=cli"}}, "claude")
	ctx := context.Background()
	SessionStart(ctx, e)
	reg, err := session.Open(project.LeadHome(f.Submodule))
	if err != nil {
		t.Fatal(err)
	}
	assertRoot := func() {
		t.Helper()
		l, live, err := reg.Lead("hook-lead")
		if err != nil || !live || l.Root != f.Linked {
			t.Fatalf("hook lead: %+v %v %v", l, live, err)
		}
	}
	assertRoot()
	e.Root, e.WorkRoot = f.Superproject, f.SuperLinked
	Prompt(ctx, e)
	assertRoot()
	e.Input = []byte(`{"session_id":"hook-lead","source":"clear","permission_mode":"plan"}`)
	SessionStart(ctx, e)
	assertRoot()
	for _, name := range []string{"sessions.json", "bus.ndjson"} {
		if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", name)); !os.IsNotExist(err) {
			t.Fatal("work-local lead state", name, err)
		}
	}
}
