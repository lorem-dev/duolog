package calibration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// testCalibration holds the default caps.
func testCalibration() config.Calibration {
	return config.Calibration{MaxWeeklyShare: 0.10, MaxWindowShare: 0.5}
}

func weekly(used float64) Window {
	return Window{Kind: KindWeekly, UsedPercent: used, ResetsAt: testTime.Add(72 * time.Hour)}
}

func fiveHour(used float64) Window {
	return Window{Kind: KindFiveHour, UsedPercent: used, ResetsAt: testTime.Add(3 * time.Hour)}
}

func TestAdmitRefusesLeadReserve(t *testing.T) {
	d := Admit("claude", []Window{weekly(80), fiveHour(10)}, CellCost{WeeklyPct: 6, WindowPct: 5}, Spend{}, testCalibration(), 0, testTime)
	if d.Admit || d.Stop != "lead reserve" {
		t.Fatalf("Admit = %+v, want Stop lead reserve", d)
	}
}

func TestAdmitFiveHourReserve(t *testing.T) {
	d := Admit("claude", []Window{weekly(20), fiveHour(86)}, CellCost{WeeklyPct: 1, WindowPct: 5}, Spend{}, testCalibration(), 0, testTime)
	if d.Admit || d.Stop != "lead reserve" {
		t.Fatalf("Admit = %+v, want Stop lead reserve", d)
	}
}

func TestAdmitApprovedCap(t *testing.T) {
	spend := Spend{WeeklyPct: map[string]float64{"claude": 9.5}}
	d := Admit("claude", []Window{weekly(30), fiveHour(10)}, CellCost{WeeklyPct: 1, WindowPct: 5}, spend, testCalibration(), 0, testTime)
	if d.Admit || d.Stop != "approved cap" {
		t.Fatalf("Admit = %+v, want Stop approved cap", d)
	}
}

func TestAdmitWindowSharePauses(t *testing.T) {
	five := fiveHour(68)
	spend := Spend{Windows: map[string]WindowSpend{"claude": {ResetsAt: five.ResetsAt, StartUsed: 20, LastUsed: 68}}}
	d := Admit("claude", []Window{weekly(30), five}, CellCost{WeeklyPct: 1, WindowPct: 5}, spend, testCalibration(), 0, testTime)
	if d.Admit || !d.Wait.Equal(five.ResetsAt) || d.Stop != "" {
		t.Fatalf("Admit = %+v, want a pause until %v", d, five.ResetsAt)
	}
}

func TestAdmitWindowShareConfiguredDown(t *testing.T) {
	five := fiveHour(56)
	spend := Spend{Windows: map[string]WindowSpend{"claude": {ResetsAt: five.ResetsAt, StartUsed: 30, LastUsed: 56}}}
	cfg := testCalibration()
	cfg.MaxWindowShare = 0.3
	d := Admit("claude", []Window{weekly(30), five}, CellCost{WeeklyPct: 1, WindowPct: 5}, spend, cfg, 0, testTime)
	if d.Admit || !d.Wait.Equal(five.ResetsAt) || d.Stop != "" {
		t.Fatalf("Admit = %+v, want a pause until %v", d, five.ResetsAt)
	}
}

func TestAdmitNoFiveHourWindow(t *testing.T) {
	// A five-hour spend left from an earlier reading must not pause a run
	// whose agent now reports no five-hour window.
	spend := Spend{Windows: map[string]WindowSpend{"codex": {ResetsAt: testTime.Add(time.Hour), StartUsed: 0, LastUsed: 90}}}
	d := Admit("codex", []Window{weekly(10)}, CellCost{WeeklyPct: 1, WindowPct: 5}, spend, testCalibration(), 0, testTime)
	if !d.Admit || d.Stop != "" || !d.Wait.IsZero() {
		t.Fatalf("Admit = %+v, want admitted", d)
	}
}

func TestAdmitQuotaUnavailable(t *testing.T) {
	q := &QuotaCache{
		Read: func(context.Context, string) ([]Window, error) { return nil, errors.New("quota endpoint down") },
		Now:  func() time.Time { return testTime },
	}
	ws, err := q.Get(context.Background(), "claude", false)
	if err == nil {
		t.Fatal("Get err = nil, want the reader error")
	}
	d := Admit("claude", ws, CellCost{WeeklyPct: 1, WindowPct: 5}, Spend{}, testCalibration(), 0, testTime)
	if d.Admit || d.Stop != "quota unavailable" {
		t.Fatalf("Admit = %+v, want Stop quota unavailable", d)
	}
}

func TestAdmitAtLimitNeverCredits(t *testing.T) {
	d := Admit("claude", []Window{weekly(100), fiveHour(10)}, CellCost{WeeklyPct: 1, WindowPct: 5}, Spend{}, testCalibration(), 0, testTime)
	if d.Admit || d.Stop != "provider limit" {
		t.Fatalf("Admit = %+v, want Stop provider limit", d)
	}
}

func TestCostOfWithoutRate(t *testing.T) {
	if got, want := CostOf(nil, 2000000, nil), (CellCost{WeeklyPct: 1, WindowPct: 5}); got != want {
		t.Fatalf("CostOf(nil) = %+v, want %+v", got, want)
	}
	// p90 of each field: weekly 2, window 4.
	measured := []CellCost{{WeeklyPct: 0.5, WindowPct: 1}, {WeeklyPct: 1, WindowPct: 2}, {WeeklyPct: 2, WindowPct: 4}}
	if got, want := CostOf(nil, 2000000, measured), (CellCost{WeeklyPct: 2, WindowPct: 5}); got != want {
		t.Fatalf("CostOf(nil, measured) = %+v, want %+v", got, want)
	}
	// Fewer than 3 measured cells do not count.
	if got, want := CostOf(nil, 2000000, measured[2:]), (CellCost{WeeklyPct: 1, WindowPct: 5}); got != want {
		t.Fatalf("CostOf(nil, one measured) = %+v, want %+v", got, want)
	}
}

func TestCostOfWithRate(t *testing.T) {
	rate := &Rate{WeeklyPerMTok: 0.5, WindowPerMTok: 3}
	if got, want := CostOf(rate, 2000000, nil), (CellCost{WeeklyPct: 1, WindowPct: 6}); got != want {
		t.Fatalf("CostOf = %+v, want %+v", got, want)
	}
}

func TestQuotaCacheTTL(t *testing.T) {
	calls := 0
	now := testTime
	q := &QuotaCache{
		Read: func(context.Context, string) ([]Window, error) {
			calls++
			return []Window{weekly(10)}, nil
		},
		Now: func() time.Time { return now },
	}
	get := func(fresh bool) {
		t.Helper()
		if _, err := q.Get(context.Background(), "claude", fresh); err != nil {
			t.Fatal(err)
		}
	}
	get(false)
	now = testTime.Add(119 * time.Second)
	get(false)
	if calls != 1 {
		t.Fatalf("calls after 0 s and 119 s = %d, want 1", calls)
	}
	now = testTime.Add(121 * time.Second)
	get(false)
	if calls != 2 {
		t.Fatalf("calls after 121 s = %d, want 2", calls)
	}
	get(true)
	get(true)
	if calls != 4 {
		t.Fatalf("calls after two fresh reads = %d, want 4", calls)
	}
}

func TestQuotaCacheDropsEntryOnFailedRead(t *testing.T) {
	calls := 0
	q := &QuotaCache{
		Read: func(context.Context, string) ([]Window, error) {
			calls++
			if calls > 1 {
				return nil, errors.New("quota endpoint down")
			}
			return []Window{weekly(10)}, nil
		},
		Now: func() time.Time { return testTime },
	}
	if _, err := q.Get(context.Background(), "claude", false); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Get(context.Background(), "claude", true); err == nil {
		t.Fatal("fresh Get = nil, want the reader error")
	}
	// The earlier reading is no longer current: a cached Get reads again.
	if ws, err := q.Get(context.Background(), "claude", false); err == nil || ws != nil {
		t.Fatalf("Get after a failed read = %v, %v; want the reader error", ws, err)
	}
}

func TestTrackStartsNewWindowAfterReset(t *testing.T) {
	old := testTime.Add(time.Hour)
	spend := Spend{Windows: map[string]WindowSpend{"claude": {ResetsAt: old, StartUsed: 10, LastUsed: 40}}}

	// Same window: the start stays, the last reading moves.
	spend.Track("claude", []Window{weekly(20), {Kind: KindFiveHour, UsedPercent: 45, ResetsAt: old}})
	if got, want := spend.Windows["claude"], (WindowSpend{ResetsAt: old, StartUsed: 10, LastUsed: 45}); got != want {
		t.Fatalf("same window = %+v, want %+v", got, want)
	}

	next := old.Add(5 * time.Hour)
	spend.Track("claude", []Window{weekly(20), {Kind: KindFiveHour, UsedPercent: 3, ResetsAt: next}})
	got := spend.Windows["claude"]
	if got.StartUsed != 3 {
		t.Fatalf("StartUsed after reset = %v, want 3", got.StartUsed)
	}
	if want := (WindowSpend{ResetsAt: next, StartUsed: 3, LastUsed: 3}); got != want {
		t.Fatalf("new window = %+v, want %+v", got, want)
	}

	// A first reading on an empty Spend starts the window too.
	var fresh Spend
	fresh.Track("codex", []Window{{Kind: KindFiveHour, UsedPercent: 7, ResetsAt: next}})
	if want := (WindowSpend{ResetsAt: next, StartUsed: 7, LastUsed: 7}); fresh.Windows["codex"] != want {
		t.Fatalf("first window = %+v, want %+v", fresh.Windows["codex"], want)
	}
}

func TestRatesRoundTrip(t *testing.T) {
	dir := stateDir(t)
	rates, err := ReadRates(dir)
	if err != nil || len(rates) != 0 {
		t.Fatalf("ReadRates without a file = %v, %v; want empty", rates, err)
	}
	want := map[string]Rate{"claude": {WeeklyPerMTok: 0.5, WindowPerMTok: 3}}
	if err := WriteRates(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadRates = %v, want %v", got, want)
	}
}
