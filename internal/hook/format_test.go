package hook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/session"
)

func TestLineForAsk(t *testing.T) {
	long := "exec " + strings.Repeat("x", 200)
	cases := []struct {
		name string
		m    bus.Message
		want string
	}{
		{"short", bus.Message{N: 15287, K: "ask", Ref: "peer-x", S: "exec git status"}, "ask 15287 peer-x: exec git status"},
		{"person", bus.Message{N: 7, K: "ask", Ref: "peer-x", P: true, S: "exec git push"}, "ask 7 peer-x: exec git push (person)"},
		{"cut", bus.Message{N: 8, K: "ask", Ref: "p", S: long}, "ask 8 p: " + long[:77] + "..."},
		{"lines joined", bus.Message{N: 9, K: "ask", Ref: "p", S: "exec a\n  b\tc"}, "ask 9 p: exec a b c"},
		{"no ref", bus.Message{N: 10, K: "ask", From: "codex/review", S: "exec ls"}, "ask 10 codex/review: exec ls"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := line(c.m)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if c.name == "cut" && len(strings.TrimPrefix(got, "ask 8 p: ")) != 80 {
				t.Fatalf("summary is %d bytes, want 80", len(strings.TrimPrefix(got, "ask 8 p: ")))
			}
		})
	}
}

func TestLineForStatusAndNotice(t *testing.T) {
	cases := []struct {
		name string
		m    bus.Message
		want string
	}{
		{"status", bus.Message{N: 3, K: "status", Ref: "peer-x", S: "pane closed"}, "status peer-x: pane closed"},
		{"status cut", bus.Message{N: 4, K: "status", Ref: "peer-x", S: strings.Repeat("é", 100)}, "status peer-x: " + strings.Repeat("é", 77) + "..."},
		{"notice", bus.Message{N: 5, K: "notice", From: "duolog/plugin", S: "plugin offered"}, "notice duolog/plugin: plugin offered"},
		{"other kind", bus.Message{N: 6, K: "done", From: "codex/review", S: "finished"}, "done 6 codex/review: finished"},
		{"no kind", bus.Message{N: 1, From: "other", S: "<exact>  bytes"}, "msg 1 other: <exact> bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := line(c.m); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if strings.Contains(line(c.m), "\n") || strings.Contains(line(c.m), `{"`) {
				t.Fatal("not one plain line")
			}
		})
	}
}

// ask sends one ask from a peer to the lead and returns its bus number.
func ask(t *testing.T, b *bus.Bus, to string, summary string) int64 {
	t.Helper()
	n, err := b.Send(bus.Message{From: "claude/worker", To: to, K: "ask", Ref: "peer-x", S: summary, W: 120})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFiveMessagesAreAllShown(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	a := session.Address(lead(t, r, e))
	for i := 0; i < 5; i++ {
		ask(t, b, a, fmt.Sprintf("exec cmd-%d", i))
	}
	c := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	lines := strings.Split(strings.TrimSuffix(c, "\n"), "\n")
	if len(lines) != 5 || strings.Contains(c, "more") || strings.Contains(c, `"n":`) {
		t.Fatal(c)
	}
	if lines[0] != "ask 1 peer-x: exec cmd-0" || lines[4] != "ask 5 peer-x: exec cmd-4" {
		t.Fatal(c)
	}
}

func TestMoreThanFiveShowsNewestThree(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	a := session.Address(lead(t, r, e))
	for i := 0; i < 100; i++ {
		ask(t, b, a, fmt.Sprintf("exec cmd-%03d", i))
	}
	c := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	want := "ask 98 peer-x: exec cmd-097\nask 99 peer-x: exec cmd-098\nask 100 peer-x: exec cmd-099\nand 97 more: duolog inbox\n"
	if c != want {
		t.Fatalf("got %q\nwant %q", c, want)
	}
	// The hidden messages stay reachable through the inbox, and the bus keeps all of them.
	lines, err := b.Inbox(a)
	if err != nil || len(lines) != 97 {
		t.Fatal(len(lines), err)
	}
	if !strings.Contains(string(lines[0]), `"n":1,`) || !strings.Contains(string(lines[96]), `"n":97,`) {
		t.Fatalf("first %q last %q", lines[0], lines[96])
	}
	if again, err := b.Inbox(a); err != nil || len(again) != 0 {
		t.Fatal(len(again), err)
	}
	all, err := os.ReadFile(filepath.Join(r.Dir, "bus.ndjson"))
	if err != nil || strings.Count(string(all), "\n") != 100 {
		t.Fatal(err)
	}
	// The next hook has nothing new and stays silent.
	silent(t, PostTool(context.Background(), e))
}

func TestSixMessagesShowThreeAndCountThree(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	a := session.Address(lead(t, r, e))
	for i := 0; i < 6; i++ {
		ask(t, b, a, fmt.Sprintf("exec cmd-%d", i))
	}
	c := content(t, PostTool(context.Background(), e), "PostToolUse")
	if strings.Count(c, "\n") != 4 || !strings.HasSuffix(c, "and 3 more: duolog inbox\n") || !strings.HasPrefix(c, "ask 4 peer-x: exec cmd-3\n") {
		t.Fatal(c)
	}
}

func TestAnsweredAskIsHidden(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	a := session.Address(lead(t, r, e))
	answered := ask(t, b, a, "exec rm -rf build")
	open := ask(t, b, a, "exec go test")
	// The lead answered the first ask before the hook ran.
	if _, err := b.Send(bus.Message{From: a, To: "claude/worker", K: "allow", Ref: strconv.FormatInt(answered, 10), S: "allow"}); err != nil {
		t.Fatal(err)
	}
	c := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	if c != fmt.Sprintf("ask %d peer-x: exec go test\n", open) {
		t.Fatal(c)
	}
}

func TestOnlyAnsweredAsksLeaveHookSilent(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	a := session.Address(lead(t, r, e))
	n := ask(t, b, a, "exec ls")
	if _, err := b.Send(bus.Message{From: a, To: "claude/worker", K: "deny", Ref: strconv.FormatInt(n, 10), S: "deny"}); err != nil {
		t.Fatal(err)
	}
	silent(t, Prompt(context.Background(), e))
}

func TestExpiredAskIsHiddenAndStatusStaysOneLine(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	a := session.Address(lead(t, r, e))
	raw := fmt.Sprintf(`{"n":1,"t":"2020-01-01T00:00:00Z","from":"claude/worker","to":%q,"k":"ask","ref":"peer-x","s":"exec old","w":120}`+"\n"+
		`{"n":2,"t":"2020-01-01T00:00:01Z","from":"claude/worker","to":%q,"k":"status","ref":"peer-x","s":"ask 1 expired: the worker was denied"}`+"\n", a, a)
	write(t, filepath.Join(r.Dir, "bus.ndjson"), raw)
	_ = b
	c := content(t, PostTool(context.Background(), e), "PostToolUse")
	if c != "status peer-x: ask 1 expired: the worker was denied\n" {
		t.Fatal(c)
	}
}
