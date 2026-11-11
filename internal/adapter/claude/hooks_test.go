package claude

import (
	"encoding/json"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/hook/hooktest"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestEventsBlockStop(t *testing.T) {
	if out := (Events{}).BlockStop(""); len(out) != 0 {
		t.Fatalf("empty reason blocked Stop: %s", out)
	}
	var out struct{ Decision, Reason string }
	const reason = "Keep a background wait for peer-busy."
	data := (Events{}).BlockStop(reason)
	if err := json.Unmarshal(data, &out); err != nil || out.Decision != "block" || out.Reason != reason {
		t.Fatalf("bad Stop decision: %s %v", data, err)
	}
}

func TestEventsContract(t *testing.T) {
	hooktest.EventsContract(t, func(*testing.T) hooktest.Events { return Events{} })
}

func TestEventsModeReadsThePayload(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          mode.Mode
		ok            bool
	}{
		{"lead", `{"session_id":"s","permission_mode":"acceptEdits"}`, mode.Edits, true},
		{"subagent", `{"session_id":"s","permission_mode":"bypassPermissions","agent_id":"sub-1"}`, "", false},
		{"no mode", `{"session_id":"s"}`, "", false},
		{"not json", `nope`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := (Events{}).Mode([]byte(tc.payload), "ignored")
			if got != tc.want || ok != tc.ok {
				t.Errorf("Mode = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestEventsShowAddsASystemMessage(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal((Events{}).Show(nil, "duolog: x", false), &got); err != nil || got["systemMessage"] != "duolog: x" || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	withReason := (Events{}).Show((Events{}).BlockStop("r"), "duolog: x", false)
	if err := json.Unmarshal(withReason, &got); err != nil || got["decision"] != "block" || got["reason"] != "r" || got["systemMessage"] != "duolog: x" {
		t.Fatalf("%s %v", withReason, err)
	}
}

// An alert adds an OSC 9 terminalSequence, which Claude Code writes to the
// person's terminal; the text in it has no control characters left.
func TestShowAlert(t *testing.T) {
	show := func(text string, alert bool) map[string]any {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal((Events{}).Show(nil, text, alert), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := show("duolog: run r2 done", true)
	if got["terminalSequence"] != "\x1b]9;duolog: run r2 done\a" || got["systemMessage"] != "duolog: run r2 done" {
		t.Fatalf("alert: %q", got)
	}
	if _, ok := show("duolog: run r2 done", false)["terminalSequence"]; ok {
		t.Fatal("no alert asked, but a terminal sequence came")
	}
	got = show("duolog: \x1b]0;title\a run\u009b r2\n done", true)
	if got["terminalSequence"] != "\x1b]9;duolog: ]0;title run r2 done\a" {
		t.Fatalf("not cleaned: %q", got["terminalSequence"])
	}
}
