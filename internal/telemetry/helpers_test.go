package telemetry

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

var testNames = Names{Roles: []string{"reviewer", "writer"}, Classes: []string{"small", "large"}}

func sampleRun() Run {
	return Run{Role: "reviewer", Class: "small", Agent: "claude", Family: "opus", Effort: "high", Placement: "worker",
		Attempt: 1, In: 1200, Cached: 800, Out: 300, UsageKnown: true, USDmicro: 45000, Priced: true, Secs: 42,
		Outcome: "done", Err: "none", Asks: 2, Denials: 1, Escalated: true, Reason: "balance"}
}

func sampleQuota() Quota {
	return Quota{Agent: "codex", Src: "app_server", Plan: "plus", Epoch: 3, Windows: []Window{
		{Kind: "five_hour", LenMin: 300, UsedBP: 1300, ResetInMin: 120, Family: "sol", Status: "ok"},
		{Kind: "weekly", LenMin: 10080, UsedBP: 2000, ResetInMin: 5000, Family: "terra", Status: "blocked"},
	}, SpendControl: true}
}

func sampleRoute() Route {
	return Route{Role: "writer", Class: "large",
		Candidates: []Target{{Agent: "claude", Family: "opus", Effort: "high"}, {Agent: "codex", Family: "sol", Effort: "medium"}},
		Chosen:     Target{Agent: "codex", Family: "sol", Effort: "medium"},
		Incumbent:  Target{Agent: "claude", Family: "opus", Effort: "high"},
		Rule:       "hysteresis", ScoreMin: 12.5}
}

func sampleGate() Gate {
	return Gate{Kind: "read", RuleClass: "outside", Answer: "deny", By: "person", WaitSecs: 7}
}

func samplePeer() Peer { return Peer{Op: "send", Agent: "codex", Secs: 3, Outcome: "done"} }

func sampleReset() Reset { return Reset{Agent: "claude", Reason: "drift"} }

func sampleEvents() []Event {
	return []Event{sampleRun(), sampleQuota(), sampleRoute(), sampleGate(), samplePeer(), sampleReset()}
}

func mustEncode(t *testing.T, e Event, names Names) []byte {
	t.Helper()
	line, err := Encode(e, names, testNow)
	if err != nil {
		t.Fatalf("Encode(%T): %v", e, err)
	}
	return line
}

func decodeLine(t *testing.T, line []byte) map[string]any {
	t.Helper()
	if len(line) == 0 || line[len(line)-1] != '\n' || slices.Contains(line[:len(line)-1], '\n') {
		t.Fatalf("not one line: %q", line)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return m
}

// step is one hop from an event to a string field: a struct field index, or
// the first element of a slice.
type step struct {
	field int
	elem  bool
}

// stringSlot is one string field of an event and its JSON path.
type stringSlot struct {
	steps []step
	keys  []string
}

func (s stringSlot) String() string { return strings.Join(s.keys, ".") }

// stringSlots finds every exported string field of e, nested structs and the
// first element of slices included, so a new field cannot skip the tests.
func stringSlots(e Event) []stringSlot {
	var out []stringSlot
	var walk func(t reflect.Type, steps []step, keys []string)
	walk = func(t reflect.Type, steps []step, keys []string) {
		switch t.Kind() {
		case reflect.String:
			out = append(out, stringSlot{steps: slices.Clone(steps), keys: slices.Clone(keys)})
		case reflect.Struct:
			for i := range t.NumField() {
				f := t.Field(i)
				if !f.IsExported() {
					continue
				}
				next := slices.Clone(keys)
				if !f.Anonymous {
					next = append(next, strings.Split(f.Tag.Get("json"), ",")[0])
				}
				walk(f.Type, append(slices.Clone(steps), step{field: i}), next)
			}
		case reflect.Slice:
			walk(t.Elem(), append(slices.Clone(steps), step{elem: true}), append(slices.Clone(keys), "0"))
		}
	}
	walk(reflect.TypeOf(e), nil, nil)
	return out
}

// with returns a copy of e with the slot set to value; slices are copied, so
// e keeps its own values.
func (s stringSlot) with(e Event, value string) Event {
	v := reflect.New(reflect.TypeOf(e)).Elem()
	v.Set(reflect.ValueOf(e))
	var set func(v reflect.Value, steps []step)
	set = func(v reflect.Value, steps []step) {
		if len(steps) == 0 {
			v.SetString(value)
			return
		}
		if steps[0].elem {
			c := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			reflect.Copy(c, v)
			v.Set(c)
			set(c.Index(0), steps[1:])
			return
		}
		set(v.Field(steps[0].field), steps[1:])
	}
	set(v, s.steps)
	return v.Interface().(Event)
}

// lookup follows a JSON path through a decoded line.
func lookup(t *testing.T, m map[string]any, keys []string) any {
	t.Helper()
	var v any = m
	for _, k := range keys {
		switch node := v.(type) {
		case map[string]any:
			v = node[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i >= len(node) {
				t.Fatalf("bad path %v", keys)
			}
			v = node[i]
		default:
			t.Fatalf("bad path %v", keys)
		}
	}
	return v
}

func pow10(n int) int64 {
	v := int64(1)
	for range n {
		v *= 10
	}
	return v
}
