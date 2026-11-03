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

func TestNewSessionRejectsMissingIdentity(t *testing.T) {
	for _, raw := range []string{`{}`, `{"block_ids":["block:first"]}`, `{"session_id":"session:peers"}`, `not JSON`} {
		c := install(t, testbin.Scenario{Stdout: raw})
		if _, err := c.NewSession(context.Background(), "duolog | project", []string{"agent"}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
