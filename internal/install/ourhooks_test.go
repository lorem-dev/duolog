package install

import (
	"maps"
	"slices"
	"testing"
)

func TestOurHooksKeepsOnlyDuologsPart(t *testing.T) {
	ours := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"duolog","args":["hook","Stop","--agent","claude"]}]}]}}`
	// The same hooks, with a foreign handler in their group, a foreign group,
	// another key and another layout.
	mixed := `{
    "model": "x",
    "hooks": {
        "Notification": [{"hooks": [{"type": "command", "command": "other"}]}],
        "Stop": [
            {"hooks": [{"command": "duolog", "type": "command", "args": ["hook", "Stop", "--agent", "claude"]}]},
            {"hooks": [{"type": "command", "command": "other-stop"}]}
        ]
    }
}`
	a, err := OurHooks([]byte(ours))
	if err != nil {
		t.Fatal(err)
	}
	b, err := OurHooks([]byte(mixed))
	if err != nil {
		t.Fatal(err)
	}
	if len(a["Stop"]) != 1 || !maps.EqualFunc(a, b, slices.Equal) {
		t.Fatalf("%v\n%v", a, b)
	}
	if none, err := OurHooks(nil); err != nil || len(none) != 0 {
		t.Fatal(none, err)
	}
	if _, err := OurHooks([]byte("[]")); err == nil {
		t.Fatal("a JSON array is not a settings file")
	}
	// A changed timeout is a difference.
	slow, _ := OurHooks([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog","args":["gate"],"timeout":150}]}]}}`))
	fast, _ := OurHooks([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog","args":["gate"],"timeout":5}]}]}}`))
	if maps.EqualFunc(slow, fast, slices.Equal) {
		t.Fatal("timeouts compare equal")
	}
}
