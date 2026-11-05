package telemetry

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestEventSchema(t *testing.T) {
	const head = `{"v":1,"t":1793880000,"type":`
	want := map[string]string{
		"run":   head + `"run","role":"reviewer","class":"small","agent":"claude","family":"opus","effort":"high","placement":"worker","attempt":1,"in":1200,"cached":800,"out":300,"usage_known":true,"usd_micro":45000,"priced":true,"secs":42,"outcome":"done","err":"none","asks":2,"denials":1,"escalated":true,"moved":false,"reason":"balance"}` + "\n",
		"quota": head + `"quota","agent":"codex","src":"app_server","plan":"plus","epoch":3,"windows":[{"kind":"five_hour","len_min":300,"used_bp":1300,"reset_in_min":120,"family":"sol","status":"ok"},{"kind":"weekly","len_min":10080,"used_bp":2000,"reset_in_min":5000,"family":"terra","status":"blocked"}],"overage":false,"spend_control":true}` + "\n",
		"route": head + `"route","role":"writer","class":"large","candidates":[{"agent":"claude","family":"opus","effort":"high"},{"agent":"codex","family":"sol","effort":"medium"}],"chosen":{"agent":"codex","family":"sol","effort":"medium"},"incumbent":{"agent":"claude","family":"opus","effort":"high"},"rule":"hysteresis","score_min":12.5}` + "\n",
		"gate":  head + `"gate","kind":"read","rule_class":"outside","answer":"deny","by":"person","wait_secs":7}` + "\n",
		"peer":  head + `"peer","op":"send","agent":"codex","secs":3,"outcome":"done"}` + "\n",
		"reset": head + `"reset","agent":"claude","reason":"drift"}` + "\n",
	}
	for _, e := range sampleEvents() {
		line := string(mustEncode(t, e, testNames))
		typ := decodeLine(t, []byte(line))["type"].(string)
		if line != want[typ] {
			t.Errorf("%s:\n got %s\nwant %s", typ, line, want[typ])
		}
	}

	// Encode owns the common metadata, whatever the caller put there.
	r := sampleRun()
	r.Common = Common{V: 9, T: 5, Type: "gate"}
	if got := string(mustEncode(t, r, testNames)); got != want["run"] {
		t.Errorf("caller metadata kept: %s", got)
	}
	if got := string(mustEncode(t, &r, testNames)); got != want["run"] {
		t.Errorf("pointer event: %s", got)
	}
	var nilRun *Run
	if _, err := Encode(nilRun, testNames, testNow); err == nil {
		t.Error("nil event encoded")
	}
	if _, err := Encode(nil, testNames, testNow); err == nil {
		t.Error("nil interface encoded")
	}

	// Ids are 32 lowercase hex characters, written only when present.
	hexID := regexp.MustCompile(`^[0-9a-f]{32}$`)
	run, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	task, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if !hexID.MatchString(run.value) || !hexID.MatchString(task.value) || run == task {
		t.Fatalf("ids %q %q", run.value, task.value)
	}
	r = sampleRun()
	r.Run, r.Task = run, task
	line := string(mustEncode(t, r, testNames))
	if prefix := head + `"run","run":"` + run.value + `","task":"` + task.value + `","role":`; !strings.HasPrefix(line, prefix) {
		t.Errorf("ids: %s", line)
	}
	g := sampleGate()
	g.Task = task
	m := decodeLine(t, mustEncode(t, g, testNames))
	if _, ok := m["run"]; ok || m["task"] != task.value {
		t.Errorf("only the task id: %v", m)
	}
	var back Run
	if err := json.Unmarshal([]byte(line), &back); err != nil || back.Run != run || back.Task != task {
		t.Errorf("read back ids: %v %v %v", err, back.Run, back.Task)
	}
	for _, bad := range []string{`""`, `"0123"`, `"` + strings.ToUpper(run.value) + `"`, `"` + run.value + `0"`, `{}`, `1`} {
		var id ID
		if err := json.Unmarshal([]byte(bad), &id); err == nil {
			t.Errorf("id %s accepted", bad)
		}
	}
	if _, err := json.Marshal(ID{}); err == nil {
		t.Error("empty id marshaled")
	}
	r = sampleRun()
	r.Run = ID{value: "not-hex"}
	if _, err := Encode(r, testNames, testNow); err == nil {
		t.Error("invalid id encoded")
	}

	// Families only: every family passes, anything else is other.
	for _, family := range []string{"fable", "opus", "sonnet", "haiku", "astra", "sol", "terra", "luna"} {
		r := sampleRun()
		r.Family = family
		q := sampleQuota()
		q.Windows[0].Family = family
		rt := sampleRoute()
		rt.Chosen.Family = family
		if got := decodeLine(t, mustEncode(t, r, testNames))["family"]; got != family {
			t.Errorf("run family %s: %v", family, got)
		}
		if got := lookup(t, decodeLine(t, mustEncode(t, q, testNames)), []string{"windows", "0", "family"}); got != family {
			t.Errorf("window family %s: %v", family, got)
		}
		if got := lookup(t, decodeLine(t, mustEncode(t, rt, testNames)), []string{"chosen", "family"}); got != family {
			t.Errorf("target family %s: %v", family, got)
		}
	}
	for _, model := range []string{"claude-opus", "codex", "gpt", "Opus", "opus ", "sonnet-latest"} {
		r := sampleRun()
		r.Family = model
		if got := decodeLine(t, mustEncode(t, r, testNames))["family"]; got != "other" {
			t.Errorf("family %q: %v", model, got)
		}
	}

	// An absent window list is an empty array, not null.
	q := sampleQuota()
	q.Windows = nil
	if line := string(mustEncode(t, q, testNames)); !strings.Contains(line, `"windows":[]`) {
		t.Errorf("nil windows: %s", line)
	}
	rt := sampleRoute()
	rt.Candidates = nil
	if line := string(mustEncode(t, rt, testNames)); !strings.Contains(line, `"candidates":[]`) {
		t.Errorf("nil candidates: %s", line)
	}
}

func TestEventLineLimit(t *testing.T) {
	base := func(n int) Quota {
		q := Quota{Agent: "claude", Src: "usage_read", Plan: "max", Epoch: 1}
		for range n {
			q.Windows = append(q.Windows, Window{Kind: "other", LenMin: 1, UsedBP: 1, ResetInMin: 1, Family: "other", Status: "other"})
		}
		return q
	}
	// grow widens one-digit numbers by extra digits in total.
	grow := func(q Quota, extra int) Quota {
		take := func(limit int) int {
			k := min(extra, limit)
			extra -= k
			return k
		}
		q.Epoch = pow10(take(15))
		for i := range q.Windows {
			q.Windows[i].LenMin = pow10(take(9))
			q.Windows[i].ResetInMin = pow10(take(9))
			q.Windows[i].UsedBP = pow10(take(4))
		}
		if extra > 0 {
			t.Fatal("no room to grow")
		}
		return q
	}
	n := 0
	for ; n < maxItems; n++ {
		if _, err := Encode(base(n+1), testNames, testNow); err != nil {
			break
		}
	}
	pad := maxLine - len(mustEncode(t, base(n), testNames))

	line, err := Encode(grow(base(n), pad), testNames, testNow)
	if err != nil || len(line) != 1024 || line[1023] != '\n' {
		t.Fatalf("1024-byte record: %d bytes, %v", len(line), err)
	}
	decodeLine(t, line)
	line, err = Encode(grow(base(n), pad+1), testNames, testNow)
	if !errors.Is(err, errLineTooLong) || line != nil {
		t.Fatalf("1025-byte record: %q, %v", line, err)
	}

	// Oversize arrays are refused, never cut short.
	rt := sampleRoute()
	for len(rt.Candidates) < 40 {
		rt.Candidates = append(rt.Candidates, rt.Chosen)
	}
	if _, err := Encode(rt, testNames, testNow); err == nil {
		t.Error("40 candidates encoded")
	}
	q := base(1000)
	if _, err := Encode(q, testNames, testNow); err == nil {
		t.Error("1000 windows encoded")
	}
}
