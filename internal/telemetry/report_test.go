package telemetry

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// reportRun is a run of role, agent and outcome with no usage, cost or
// counts; a test sets those it needs.
func reportRun(role, agent, outcome string) Run {
	return Run{Role: role, Class: "small", Agent: agent, Family: "opus", Effort: "high", Placement: "worker",
		Attempt: 1, UsageKnown: true, Priced: true, Outcome: outcome, Err: "none", Reason: "none"}
}

func lines(t *testing.T, at time.Time, events ...Event) []byte {
	t.Helper()
	var out []byte
	for _, e := range events {
		line, err := Encode(e, testNames, at)
		if err != nil {
			t.Fatalf("Encode(%T): %v", e, err)
		}
		out = append(out, line...)
	}
	return out
}

func mustReport(t *testing.T, s *Store, q Query) Report {
	t.Helper()
	r, err := s.Report(q)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	return r
}

func TestReportTotals(t *testing.T) {
	s, root := readFixture(t)
	here := filepath.Dir(projectSegment(t, root, testDay))
	there := filepath.Join(root, "v1", otherID)
	at := func(d string, hour int) time.Time {
		parsed, err := time.Parse(time.DateOnly, d)
		if err != nil {
			t.Fatal(err)
		}
		return parsed.Add(time.Duration(hour) * time.Hour)
	}

	// Yesterday, compressed: the plain copy a crash left holds one more run,
	// which a gzip copy of the day hides.
	known := reportRun("reviewer", "claude", "done")
	known.In, known.Cached, known.Out, known.USDmicro = 1000, 400, 200, 30000
	known.Secs, known.Asks, known.Denials = 10, 1, 2
	yesterday := lines(t, at("2026-11-04", 9), known)
	plant(t, filepath.Join(here, "2026-11-04.ndjson.gz"), gzipped(t, yesterday))
	plant(t, filepath.Join(here, "2026-11-04.ndjson"), append(yesterday, lines(t, at("2026-11-04", 10), known)...))

	// Today: usage known to be zero counts as known; unknown usage and an
	// unpriced run add no tokens and no cost, whatever their numbers say.
	zero := reportRun("reviewer", "claude", "done")
	zero.Secs = 5
	unknown := reportRun("reviewer", "claude", "done")
	unknown.UsageKnown, unknown.In, unknown.Cached, unknown.Out = false, 500, 100, 70
	unknown.Priced, unknown.USDmicro = false, 999
	unknown.Secs, unknown.Asks = 7, 3
	failed := reportRun("writer", "codex", "failed")
	failed.In, failed.Out, failed.USDmicro, failed.Secs, failed.Denials = 40, 4, 120, 2, 1
	plant(t, filepath.Join(here, testDay+".ndjson"), append(
		lines(t, at(testDay, 8), zero, unknown),
		lines(t, at(testDay, 9), failed)...))

	// Out of the retention window, and over a day ahead of now.
	plant(t, filepath.Join(here, "2026-10-05.ndjson"), lines(t, testNow.Add(-31*day), known))
	plant(t, filepath.Join(here, "2026-11-07.ndjson"), lines(t, testNow.Add(2*day), known))

	// Another project.
	elsewhere := reportRun("reviewer", "claude", "done")
	elsewhere.In, elsewhere.Out, elsewhere.USDmicro, elsewhere.Secs = 100, 50, 1000, 3
	plant(t, filepath.Join(there, testDay+".ndjson"), lines(t, at(testDay, 10), elsewhere))

	// Account events: quota snapshots planted out of time order, a route
	// that moved and one that stayed.
	moved := sampleRoute()
	stayed := sampleRoute()
	stayed.Chosen = stayed.Incumbent
	plant(t, accountSegment(root, testDay), append(
		lines(t, at(testDay, 10), quotaEpoch(2), moved),
		lines(t, at(testDay, 11), stayed)...))
	plant(t, accountSegment(root, "2026-11-04"), lines(t, at("2026-11-04", 9), quotaEpoch(1)))

	yesterdayRow := Daily{Day: "2026-11-04", Agent: "claude", Role: "reviewer", Outcome: "done",
		Runs: 1, In: 1000, Cached: 400, Out: 200, USDmicro: 30000, Secs: 10, Asks: 1, Denials: 2}
	failedRow := Daily{Day: testDay, Agent: "codex", Role: "writer", Outcome: "failed",
		Runs: 1, In: 40, Out: 4, USDmicro: 120, Secs: 2, Denials: 1}

	t.Run("this project", func(t *testing.T) {
		r := mustReport(t, s, Query{})
		want := []Daily{
			yesterdayRow,
			{Day: testDay, Agent: "claude", Role: "reviewer", Outcome: "done",
				Runs: 2, Secs: 12, Asks: 3, Unpriced: 1, UsageUnknown: 1},
			failedRow,
		}
		if !reflect.DeepEqual(r.Daily, want) {
			t.Errorf("daily\n got %+v\nwant %+v", r.Daily, want)
		}
	})

	t.Run("all projects", func(t *testing.T) {
		r := mustReport(t, s, Query{AllProjects: true})
		want := []Daily{
			yesterdayRow,
			{Day: testDay, Agent: "claude", Role: "reviewer", Outcome: "done",
				Runs: 3, In: 100, Out: 50, USDmicro: 1000, Secs: 15, Asks: 3, Unpriced: 1, UsageUnknown: 1},
			failedRow,
		}
		if !reflect.DeepEqual(r.Daily, want) {
			t.Errorf("daily\n got %+v\nwant %+v", r.Daily, want)
		}
		again := mustReport(t, s, Query{AllProjects: true})
		if !reflect.DeepEqual(again, r) {
			t.Error("a second report differs from the first")
		}

		var curve []int64
		var times []int64
		for _, p := range r.Quota {
			curve = append(curve, p.Quota.Epoch)
			times = append(times, p.T)
			if p.T != p.Quota.T {
				t.Errorf("quota point at %d holds a snapshot at %d", p.T, p.Quota.T)
			}
		}
		if !slices.Equal(curve, []int64{1, 2}) {
			t.Errorf("quota curve %v, want [1 2]", curve)
		}
		if !slices.IsSorted(times) {
			t.Errorf("quota times %v not in order", times)
		}

		if len(r.Moves) != 1 {
			t.Fatalf("moves %+v, want the one route that changed", r.Moves)
		}
		m := r.Moves[0]
		if m.T != at(testDay, 10).Unix() || m.Route.Chosen != moved.Chosen || m.Route.Incumbent != moved.Incumbent {
			t.Errorf("move %+v, want the route at 10:00 from %v to %v", m, moved.Incumbent, moved.Chosen)
		}
	})

	t.Run("since narrows", func(t *testing.T) {
		r := mustReport(t, s, Query{Since: at(testDay, 0)})
		if len(r.Daily) != 2 || r.Daily[0].Day != testDay || len(r.Quota) != 1 {
			t.Errorf("report since today %+v", r)
		}
	})

	t.Run("json", func(t *testing.T) {
		r := mustReport(t, s, Query{})
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"daily", "moves", "quota"}) {
			t.Errorf("report keys %v", keys)
		}
		row, err := json.Marshal(r.Daily[0])
		if err != nil {
			t.Fatal(err)
		}
		want := `{"day":"2026-11-04","agent":"claude","role":"reviewer","outcome":"done","runs":1,"in":1000,"cached":400,"out":200,"usd_micro":30000,"secs":10,"asks":1,"denials":2,"unpriced":0,"usage_unknown":0}`
		if string(row) != want {
			t.Errorf("daily row\n got %s\nwant %s", row, want)
		}
		point, err := json.Marshal(r.Quota[0])
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]json.RawMessage
		if err := json.Unmarshal(point, &p); err != nil || p["t"] == nil || p["quota"] == nil || len(p) != 2 {
			t.Errorf("quota point %s", point)
		}
		move, err := json.Marshal(r.Moves[0])
		if err != nil {
			t.Fatal(err)
		}
		var mv map[string]json.RawMessage
		if err := json.Unmarshal(move, &mv); err != nil || mv["t"] == nil || mv["route"] == nil || len(mv) != 2 {
			t.Errorf("move %s", move)
		}
	})

	t.Run("empty store", func(t *testing.T) {
		noOverride(t)
		o := storeOptions(tempBase(t))
		o.Enabled = false
		empty := mustOpenStore(t, o)
		data, err := json.Marshal(mustReport(t, empty, Query{AllProjects: true}))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"daily":[],"quota":[],"moves":[]}` {
			t.Errorf("empty report %s", data)
		}
	})
}
