package rex

import (
	"context"
	"errors"
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
	c := install(t, testbin.Scenario{Stdout: "{}", Responses: map[string]testbin.Scenario{
		"block": {Stdout: `{"block":{"window_id":"window:peer"}}`},
	}})
	c.Session = "session:peers"
	if err := c.Focus(context.Background(), "client:app", "block:peer"); err != nil {
		t.Fatal(err)
	}
	got := calls(t, c.Bin)
	want := [][]string{
		{"block", "inspect", "-s", "session:peers", "--json", "block:peer"},
		{"-C", "client:app", "do", "session.select", "session_id=session:peers", "window_id=window:peer"},
		{"-C", "client:app", "focus", "block:peer", "-s", "session:peers"},
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("%v", got)
	}
}

// A block whose window cannot be read is still focused: the select then
// names the session only, as before.
func TestFocusWithoutWindowSelectsTheSession(t *testing.T) {
	c := install(t, testbin.Scenario{Stdout: "{}"})
	c.Session = "session:peers"
	if err := c.Focus(context.Background(), "client:app", "block:peer"); err != nil {
		t.Fatal(err)
	}
	got := calls(t, c.Bin)
	if len(got) != 3 || !slices.Equal(got[1], []string{"-C", "client:app", "do", "session.select", "session_id=session:peers"}) {
		t.Fatalf("%v", got)
	}
}

// Recorded from Rex.app with Remote Control off: exit 1 and this line.
func TestFocusExplainsRemoteControlOff(t *testing.T) {
	c := install(t, testbin.Scenario{Exit: 1, Stderr: "Error: Rex.app is not accepting actions. Turn on Remote Control in its Settings under Rex Server."})
	c.Session = "session:peers"
	err := c.Focus(context.Background(), "client:app", "block:peer")
	if !errors.Is(err, ErrRemoteControl) {
		t.Fatalf("%v", err)
	}
	if got := calls(t, c.Bin); len(got) == 0 || got[len(got)-1][2] != "do" {
		t.Fatalf("focused after a refused select: %v", got)
	}
}

func TestFocusErrorNamesFocus(t *testing.T) {
	c := install(t, testbin.Scenario{Exit: 1, Stderr: "Error: client not found"})
	c.Session = "session:peers"
	err := c.Focus(context.Background(), "client:gone", "block:peer")
	if err == nil || !strings.HasPrefix(err.Error(), "rex session.select failed (exit 1): ") || !strings.Contains(err.Error(), "client not found") {
		t.Fatalf("%v", err)
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
