package rex

import (
	"context"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestNewSessionIsDetachedAndNamesFirstTab(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: `{"session_id":"session:peers","block_ids":["block:first"]}`})
	c.Session, c.Anchor, c.Dir, c.Label = "session:lead", "block:lead", "/project", "duolog | codex-planner"
	created, err := c.NewSession(context.Background(), "duolog | project", []string{"agent", "arg"})
	if err != nil || created.Session != "session:peers" || created.Block != "block:first" {
		t.Fatalf("%+v %v", created, err)
	}
	got := calls(t, c.Bin)
	want := []string{"new", "duolog | project", "--window", "duolog | codex-planner", "--json", "--shell=none", "-c", "/project", "--", "agent", "arg"}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("%v", got)
	}
	if c.Session != "session:lead" || c.Anchor != "block:lead" {
		t.Fatal("lead client changed")
	}
}

func TestKillSessionNamesOnlyTheManagedSession(t *testing.T) {
	c := install(t, testbin.Scenario{})
	c.Session = "session:peers"
	if err := c.KillSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, c.Bin); len(got) != 1 || !slices.Equal(got[0], []string{"kill", "session:peers"}) {
		t.Fatal(got)
	}
}

func TestNewSessionFindsItsOnlyBlockWhenReplyOmitsIt(t *testing.T) {
	c := install(t, testbin.Scenario{Responses: map[string]testbin.Scenario{
		"new":                              {Stdout: `{"session_id":"session:peers"}`},
		"block ls -s session:peers --json": {Stdout: `{"blocks":[{"id":"block:first"}]}`},
	}})
	c.Session = "session:lead"
	got, err := c.NewSession(context.Background(), "duolog | project", []string{"agent"})
	if err != nil || got.Block != "block:first" || got.Session != "session:peers" {
		t.Fatalf("%+v %v", got, err)
	}
	if got := calls(t, c.Bin); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestNewSessionTakesBlockFromInitialWindows(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: `{"session_id":"session:peers","initial_windows":[{"window_id":"window:W","block_ids":["block:first"],"revision":1}]}`})
	c.Session = "session:lead"
	got, err := c.NewSession(context.Background(), "duolog | project", []string{"agent"})
	if err != nil || got.Block != "block:first" || got.Session != "session:peers" {
		t.Fatalf("%+v %v", got, err)
	}
	if got := calls(t, c.Bin); len(got) != 1 {
		t.Fatal(got)
	}
}

func TestNewSessionFindsItsOnlyBlockByBlockID(t *testing.T) {
	c := install(t, testbin.Scenario{Responses: map[string]testbin.Scenario{
		"new":                              {Stdout: `{"session_id":"session:peers"}`},
		"block ls -s session:peers --json": {Stdout: `{"blocks":[{"block_id":"block:first","label":"shell","window_id":"window:W"}]}`},
	}})
	c.Session = "session:lead"
	got, err := c.NewSession(context.Background(), "duolog | project", []string{"agent"})
	if err != nil || got.Block != "block:first" || got.Session != "session:peers" {
		t.Fatalf("%+v %v", got, err)
	}
	if got := calls(t, c.Bin); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestNewSessionRejectsMissingIdentity(t *testing.T) {
	for _, raw := range []string{`{}`, `{"block_ids":["block:first"]}`, `{"session_id":"session:peers"}`, `not JSON`} {
		c := install(t, testbin.Scenario{Stdout: raw})
		if _, err := c.NewSession(context.Background(), "duolog | project", []string{"agent"}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestListSessions(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: `{"sessions":[{"session_id":"session:other","label":"shell"},{"session_id":"session:peers","label":"duolog | project"}]}`})
	got, err := c.ListSessions(context.Background())
	if err != nil || len(got) != 2 || got[0].ID != "session:other" || got[1].Label != "duolog | project" {
		t.Fatalf("%+v %v", got, err)
	}
	if got := calls(t, c.Bin); len(got) != 1 || !slices.Equal(got[0], []string{"session", "list", "--json"}) {
		t.Fatal(got)
	}
}

func TestListSessionsRejectsInvalidReplyOrCommandFailure(t *testing.T) {
	for _, scenario := range []testbin.Scenario{
		{Stdout: `not JSON`},
		{Exit: 4},
	} {
		c := install(t, scenario)
		if _, err := c.ListSessions(context.Background()); err == nil {
			t.Fatal("accepted invalid reply or failed command")
		}
	}
}

func TestListSessionsSkipsEmptyIdentity(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: `{"sessions":[{"session_id":"session:first","label":"duolog | project (1)"},{"label":"duolog | project"},{"session_id":"","label":"duolog | project"},{"session_id":"session:last","label":"duolog | project (2)"}]}`})
	got, err := c.ListSessions(context.Background())
	if err != nil || len(got) != 2 || got[0].ID != "session:first" || got[1].ID != "session:last" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestRenameSessionUsesExplicitIdentity(t *testing.T) {
	c := install(t, testbin.Scenario{})
	c.Session = "session:peers"
	if err := c.RenameSession(context.Background(), "duolog | project (1)"); err != nil {
		t.Fatal(err)
	}
	want := []string{"session", "rename", "session:peers", "duolog | project (1)"}
	if got := calls(t, c.Bin); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatal(got)
	}
}

func TestRenameSessionRejectsMissingIdentityOrLabel(t *testing.T) {
	for _, values := range [][2]string{{"", "duolog | project"}, {"session:peers", ""}} {
		c := install(t, testbin.Scenario{})
		c.Session = values[0]
		if err := c.RenameSession(context.Background(), values[1]); err == nil {
			t.Fatal("accepted missing session or label")
		}
		if got := calls(t, c.Bin); len(got) != 0 {
			t.Fatal(got)
		}
	}
}

func TestRenameSessionReportsFailure(t *testing.T) {
	c := install(t, testbin.Scenario{Exit: 4})
	if err := c.RenameSession(context.Background(), "duolog | project"); err == nil {
		t.Fatal("failed rename succeeded")
	}
}
