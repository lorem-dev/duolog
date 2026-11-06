package remember_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/remember"
	"github.com/lorem-dev/duolog/internal/app/remember/remembertest"
)

func TestAllowPrintsOneLineForEachAgent(t *testing.T) {
	first := &remembertest.Fake{Exact: "Bash(ls)"}
	second := &remembertest.Fake{Exact: "no rule; exact"}
	var out bytes.Buffer
	agents := []remember.Agent{{Name: "claude", Allow: first}, {Name: "codex", Allow: second}}
	if err := remember.Allow(&out, "/p", "ls", agents); err != nil {
		t.Fatal(err)
	}
	if want := "claude: Bash(ls)\ncodex: no rule; exact\n"; out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
	if len(first.Calls) != 1 || first.Calls[0] != "allow ls" {
		t.Fatalf("calls: %v", first.Calls)
	}
}

func TestNilPortPrintsNoRule(t *testing.T) {
	var out bytes.Buffer
	agents := []remember.Agent{{Name: "kestrel"}}
	if err := remember.AllowMCP(&out, "/p", "tracker", "get_issue", agents); err != nil {
		t.Fatal(err)
	}
	if out.String() != "kestrel: no rule\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestErrorStopsBeforeTheNextAgent(t *testing.T) {
	boom := errors.New("boom")
	next := &remembertest.Fake{}
	var out bytes.Buffer
	agents := []remember.Agent{{Name: "a", Allow: &remembertest.Fake{Err: boom}}, {Name: "b", Allow: next}}
	if err := remember.Allow(&out, "/p", "ls", agents); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if len(next.Calls) != 0 || strings.Contains(out.String(), "b:") {
		t.Fatalf("the next agent ran: %v %q", next.Calls, out.String())
	}
}
