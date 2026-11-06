package rex

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestClientsParsesRecordedReply(t *testing.T) {
	data, err := os.ReadFile("testdata/client-ls.json")
	if err != nil {
		t.Fatal(err)
	}
	c := install(t, testbin.Scenario{Responses: map[string]testbin.Scenario{"client ls": {Stdout: string(data)}}})
	got, err := c.Clients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	for _, info := range got {
		if info.ID == "" {
			t.Fatalf("empty ID: %+v", info)
		}
	}
	app := got[0]
	if app.Kind != "app" || app.Name != "Laptop" || len(app.Sessions) != 2 ||
		app.Sessions[0] != "session:01a00000-0000-7000-8000-0000000000aa" {
		t.Fatalf("%+v", app)
	}
	if got[1].Kind != "" || len(got[1].Sessions) != 0 {
		t.Fatalf("client without info: %+v", got[1])
	}
	if got[2].Kind != "cli" || got[2].Name != "rex" {
		t.Fatalf("%+v", got[2])
	}
	calls := calls(t, c.Bin)
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"client", "ls", "--json"}) {
		t.Fatalf("%v", calls)
	}
}

func TestClientsRejectsBadReply(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: "not json"})
	_, err := c.Clients(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rex client ls") {
		t.Fatalf("%v", err)
	}
}

func TestFocusArgs(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: "{}"})
	c.Session = "session:peers"
	if err := c.Focus(context.Background(), "client:app", "block:peer"); err != nil {
		t.Fatal(err)
	}
	got := calls(t, c.Bin)
	want := []string{"-C", "client:app", "focus", "block:peer", "-s", "session:peers"}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("%v", got)
	}
}

func TestFocusNeedsClientBlockAndSession(t *testing.T) {
	c := install(t, testbin.Scenario{})
	c.Session = "session:peers"
	if c.Focus(context.Background(), "", "block:peer") == nil ||
		c.Focus(context.Background(), "client:app", "") == nil {
		t.Fatal("want errors")
	}
	c.Session = ""
	if c.Focus(context.Background(), "client:app", "block:peer") == nil {
		t.Fatal("want an error without a session")
	}
	if got := calls(t, c.Bin); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
