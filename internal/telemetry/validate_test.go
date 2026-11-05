package telemetry

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestClosedFields(t *testing.T) {
	hostile := []string{
		"",
		"DONE",
		"sk-ant-api03-SENTINEL",
		"\x1b[31mSENTINEL\x1b[0m",
		"done\nSENTINEL",
		"reviewer\n",
		`done","SENTINEL":"x`,
		`{"SENTINEL":1}`,
		strings.Repeat("SENTINEL", 200),
	}
	// Configured names that fail the name pattern are still other.
	names := Names{
		Roles:   append([]string{"reviewer", "Reviewer", strings.Repeat("r", 33)}, hostile...),
		Classes: append([]string{"small", "Small", "a b"}, hostile...),
	}
	slots := 0
	for _, e := range sampleEvents() {
		typ := decodeLine(t, mustEncode(t, e, testNames))["type"]
		for _, slot := range stringSlots(e) {
			slots++
			for _, bad := range hostile {
				ev := slot.with(e, bad)
				line, err := Encode(ev, names, testNow)
				if err != nil {
					t.Fatalf("%T %s %q: %v", e, slot, bad, err)
				}
				if bytes.Contains(line, []byte("SENTINEL")) || bytes.Contains(line, []byte("\x1b")) || bytes.Contains(line, []byte(`\u001b`)) {
					t.Errorf("%T %s leaked: %s", e, slot, line)
				}
				want := any("other")
				if slot.String() == "type" {
					want = typ
				}
				if got := lookup(t, decodeLine(t, line), slot.keys); got != want {
					t.Errorf("%T %s %q: got %v, want %v", e, slot, bad, got, want)
				}
				if bad != "" && reflect.DeepEqual(ev, e) {
					t.Fatalf("%T %s: slot not set", e, slot)
				}
			}
		}
	}
	if slots != 42 {
		t.Errorf("found %d string slots, want 42", slots)
	}

	// Role and class pass only when configured and well formed.
	cases := []struct {
		role, class string
		names       Names
		role2       string
		class2      string
	}{
		{"reviewer", "small", testNames, "reviewer", "small"},
		{"auditor", "huge", testNames, "other", "other"},
		{"Reviewer", "Small", names, "other", "other"},
		{strings.Repeat("r", 33), "a b", names, "other", "other"},
		{"a.b_c-9", strings.Repeat("c", 32), Names{Roles: []string{"a.b_c-9"}, Classes: []string{strings.Repeat("c", 32)}}, "a.b_c-9", strings.Repeat("c", 32)},
		{"reviewer", "small", Names{}, "other", "other"},
	}
	for _, c := range cases {
		r := sampleRun()
		r.Role, r.Class = c.role, c.class
		rt := sampleRoute()
		rt.Role, rt.Class = c.role, c.class
		for _, e := range []Event{r, rt} {
			m := decodeLine(t, mustEncode(t, e, c.names))
			if m["role"] != c.role2 || m["class"] != c.class2 {
				t.Errorf("%T %q/%q: got %v/%v", e, c.role, c.class, m["role"], m["class"])
			}
		}
	}

	// Every closed set value passes as itself.
	sets := map[string][]string{
		"placement": {"worker", "peer", "self", "inlead", "other"},
		"outcome":   {"done", "failed", "infra", "lead", "interrupted", "needs", "other"},
		"err":       {"none", "quota", "auth", "timeout", "exit", "parse", "gate", "other"},
		"reason":    {"explicit", "availability", "cross_review", "pinned", "admission", "hysteresis", "balance", "outside_ladder", "none", "quota", "auth", "timeout", "gate", "other"},
		"agent":     {"claude", "codex", "other"},
		"effort":    {"low", "medium", "high", "xhigh", "max", "ultra", "other"},
	}
	for field, values := range sets {
		for _, v := range values {
			r := sampleRun()
			reflect.ValueOf(&r).Elem().FieldByNameFunc(func(n string) bool { return strings.EqualFold(n, field) }).SetString(v)
			if got := decodeLine(t, mustEncode(t, r, testNames))[field]; got != v {
				t.Errorf("run %s %q: %v", field, v, got)
			}
		}
	}
	quota := map[string][]string{
		"src":  {"usage_read", "app_server", "rate_limit_event", "other"},
		"plan": {"free", "pro", "max", "plus", "team", "business", "enterprise", "edu", "other"},
	}
	for field, values := range quota {
		for _, v := range values {
			q := sampleQuota()
			reflect.ValueOf(&q).Elem().FieldByNameFunc(func(n string) bool { return strings.EqualFold(n, field) }).SetString(v)
			if got := decodeLine(t, mustEncode(t, q, testNames))[field]; got != v {
				t.Errorf("quota %s %q: %v", field, v, got)
			}
		}
	}
	for _, kind := range []string{"five_hour", "weekly", "other"} {
		for _, status := range []string{"ok", "blocked", "unknown", "other"} {
			q := sampleQuota()
			q.Windows[0].Kind, q.Windows[0].Status = kind, status
			w := lookup(t, decodeLine(t, mustEncode(t, q, testNames)), []string{"windows", "0"}).(map[string]any)
			if w["kind"] != kind || w["status"] != status {
				t.Errorf("window %s/%s: %v", kind, status, w)
			}
		}
	}
	for _, rule := range []string{"explicit", "availability", "cross_review", "pinned", "admission", "hysteresis", "balance", "outside_ladder", "other"} {
		rt := sampleRoute()
		rt.Rule = rule
		if got := decodeLine(t, mustEncode(t, rt, testNames))["rule"]; got != rule {
			t.Errorf("rule %q: %v", rule, got)
		}
	}
	for _, rule := range []string{"none", "quota"} {
		rt := sampleRoute()
		rt.Rule = rule
		if got := decodeLine(t, mustEncode(t, rt, testNames))["rule"]; got != "other" {
			t.Errorf("route rule %q: %v", rule, got)
		}
	}
	gates := []Gate{}
	for _, kind := range []string{"read", "write", "exec", "net", "mcp", "unknown", "other"} {
		gates = append(gates, Gate{Kind: kind, RuleClass: "mcp", Answer: "allow", By: "auto"})
	}
	for _, rc := range []string{"outside", "on_unmatched", "mode", "mcp", "unverified", "other"} {
		gates = append(gates, Gate{Kind: "net", RuleClass: rc, Answer: "timeout", By: "lead"})
	}
	for _, by := range []string{"person", "lead", "auto", "other"} {
		gates = append(gates, Gate{Kind: "exec", RuleClass: "mode", Answer: "other", By: by})
	}
	for _, g := range gates {
		want := fmt.Sprintf(`"kind":%q,"rule_class":%q,"answer":%q,"by":%q`, g.Kind, g.RuleClass, g.Answer, g.By)
		if line := string(mustEncode(t, g, testNames)); !strings.Contains(line, want) {
			t.Errorf("gate %s: %s", want, line)
		}
	}
	for _, op := range []string{"open", "send", "close", "other"} {
		for _, outcome := range []string{"done", "failed", "infra", "lead", "interrupted", "needs", "other"} {
			want := fmt.Sprintf(`"op":%q,"agent":"claude","secs":0,"outcome":%q`, op, outcome)
			if line := string(mustEncode(t, Peer{Op: op, Agent: "claude", Outcome: outcome}, testNames)); !strings.Contains(line, want) {
				t.Errorf("peer %s: %s", want, line)
			}
		}
	}
	for _, reason := range []string{"account", "plan", "drift", "manual", "other"} {
		if got := decodeLine(t, mustEncode(t, Reset{Agent: "codex", Reason: reason}, testNames))["reason"]; got != reason {
			t.Errorf("reset reason %q: %v", reason, got)
		}
	}

	// Encode cleans a copy; the caller's event and slices stay as they were.
	q := sampleQuota()
	q.Windows[0].Family = "SENTINEL"
	rt := sampleRoute()
	rt.Candidates[0].Agent = "SENTINEL"
	mustEncode(t, q, testNames)
	mustEncode(t, rt, testNames)
	if q.Windows[0].Family != "SENTINEL" || rt.Candidates[0].Agent != "SENTINEL" {
		t.Error("Encode changed the caller's slices")
	}
}

func TestEventNumbers(t *testing.T) {
	const maxSafe, max31 = 1<<53 - 1, 1<<31 - 1
	run := func(f func(*Run)) Event { r := sampleRun(); f(&r); return r }
	window := func(f func(*Window)) Event { q := sampleQuota(); f(&q.Windows[1]); return q }
	bounds := []struct {
		name   string
		lo, hi int64
		set    func(n int64) Event
	}{
		{"in", 0, maxSafe, func(n int64) Event { return run(func(r *Run) { r.In, r.Cached = n, 0 }) }},
		{"cached", 0, maxSafe, func(n int64) Event { return run(func(r *Run) { r.In, r.Cached = maxSafe, n }) }},
		{"out", 0, maxSafe, func(n int64) Event { return run(func(r *Run) { r.Out = n }) }},
		{"usd_micro", 0, maxSafe, func(n int64) Event { return run(func(r *Run) { r.USDmicro = n }) }},
		{"asks", 0, maxSafe, func(n int64) Event { return run(func(r *Run) { r.Asks = n }) }},
		{"denials", 0, maxSafe, func(n int64) Event { return run(func(r *Run) { r.Denials = n }) }},
		{"attempt", 1, 1_000_000, func(n int64) Event { return run(func(r *Run) { r.Attempt = int(n) }) }},
		{"run secs", 0, max31, func(n int64) Event { return run(func(r *Run) { r.Secs = n }) }},
		{"epoch", 0, maxSafe, func(n int64) Event { q := sampleQuota(); q.Epoch = n; return q }},
		{"len_min", 1, max31, func(n int64) Event { return window(func(w *Window) { w.LenMin = n }) }},
		{"used_bp", 0, 10_000, func(n int64) Event { return window(func(w *Window) { w.UsedBP = n }) }},
		{"reset_in_min", 0, max31, func(n int64) Event { return window(func(w *Window) { w.ResetInMin = n }) }},
		{"wait_secs", 0, max31, func(n int64) Event { g := sampleGate(); g.WaitSecs = n; return g }},
		{"peer secs", 0, max31, func(n int64) Event { p := samplePeer(); p.Secs = n; return p }},
	}
	for _, b := range bounds {
		for _, c := range []struct {
			n  int64
			ok bool
		}{{b.lo, true}, {b.hi, true}, {b.lo - 1, false}, {b.hi + 1, false}, {math.MinInt64, false}, {math.MaxInt64, false}} {
			line, err := Encode(b.set(c.n), testNames, testNow)
			if c.ok && err != nil {
				t.Errorf("%s=%d rejected: %v", b.name, c.n, err)
			}
			if !c.ok && (err == nil || line != nil) {
				t.Errorf("%s=%d accepted: %s", b.name, c.n, line)
			}
			if c.ok && err == nil {
				if !strings.Contains(string(line), fmt.Sprintf(":%d", c.n)) {
					t.Errorf("%s=%d not written as is: %s", b.name, c.n, line)
				}
			}
		}
	}

	// Cached tokens never exceed input tokens.
	if _, err := Encode(run(func(r *Run) { r.In, r.Cached = 10, 10 }), testNames, testNow); err != nil {
		t.Errorf("cached == in: %v", err)
	}
	if _, err := Encode(run(func(r *Run) { r.In, r.Cached = 10, 11 }), testNames, testNow); err == nil {
		t.Error("cached > in accepted")
	}

	score := func(s float64) Event { rt := sampleRoute(); rt.ScoreMin = s; return rt }
	for _, s := range []float64{0, -1e9, 1e9, 0.25, -12.5} {
		if _, err := Encode(score(s), testNames, testNow); err != nil {
			t.Errorf("score_min %v rejected: %v", s, err)
		}
	}
	for _, s := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.Nextafter(1e9, math.Inf(1)), math.Nextafter(-1e9, math.Inf(-1)), math.MaxFloat64} {
		if line, err := Encode(score(s), testNames, testNow); err == nil {
			t.Errorf("score_min %v accepted: %s", s, line)
		}
	}
}
