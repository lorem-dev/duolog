package runner

import (
	"context"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// Claude names the minute and the zone of its five-hour reset.
func TestClaudeResetWithMinutesAndZone(t *testing.T) {
	const text = "You've hit your 5-hour limit · resets 8:40pm (Europe/London)"
	london, newYork, tokyo := zone(t, "Europe/London"), zone(t, "America/New_York"), zone(t, "Asia/Tokyo")
	for _, tc := range []struct {
		name      string
		now, want time.Time
	}{
		{"before", time.Date(2026, 11, 4, 15, 0, 0, 0, london), time.Date(2026, 11, 4, 20, 40, 0, 0, london)},
		{"at", time.Date(2026, 11, 4, 20, 40, 0, 0, london), time.Date(2026, 11, 5, 20, 40, 0, 0, london)},
		{"after", time.Date(2026, 11, 4, 21, 0, 0, 0, london), time.Date(2026, 11, 5, 20, 40, 0, 0, london)},
		{"after midnight", time.Date(2026, 11, 5, 0, 30, 0, 0, london), time.Date(2026, 11, 5, 20, 40, 0, 0, london)},
		{"across the year", time.Date(2026, 12, 31, 23, 59, 0, 0, london), time.Date(2027, 1, 1, 20, 40, 0, 0, london)},
		// New York is five hours behind London in November, Tokyo nine ahead.
		{"other zone before", time.Date(2026, 11, 4, 10, 0, 0, 0, newYork), time.Date(2026, 11, 4, 20, 40, 0, 0, london)},
		{"other zone after", time.Date(2026, 11, 4, 16, 0, 0, 0, newYork), time.Date(2026, 11, 5, 20, 40, 0, 0, london)},
		{"other zone next day", time.Date(2026, 11, 5, 5, 0, 0, 0, tokyo), time.Date(2026, 11, 4, 20, 40, 0, 0, london)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResetTime(text, tc.now)
			if !got.Equal(tc.want) || got.Location().String() != "Europe/London" {
				t.Fatalf("%s want %s", got, tc.want)
			}
		})
	}
}

// Without a zone the time is the local time of now.
func TestClaudeResetWithoutZone(t *testing.T) {
	const text = "You've hit your 5-hour limit · resets 8:40pm"
	tokyo := zone(t, "Asia/Tokyo")
	for _, tc := range []struct{ now, want time.Time }{
		{time.Date(2026, 11, 4, 10, 0, 0, 0, tokyo), time.Date(2026, 11, 4, 20, 40, 0, 0, tokyo)},
		{time.Date(2026, 11, 4, 21, 0, 0, 0, tokyo), time.Date(2026, 11, 5, 20, 40, 0, 0, tokyo)},
	} {
		if got := ResetTime(text, tc.now); !got.Equal(tc.want) || got.Location() != tokyo {
			t.Fatalf("%s: %s want %s", tc.now, got, tc.want)
		}
	}
	// The error type in parentheses is not a zone.
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, tokyo)
	if got, want := ResetTime(text+" (error type rate_limit, HTTP 429)", now), time.Date(2026, 11, 4, 20, 40, 0, 0, tokyo); !got.Equal(want) {
		t.Fatalf("%s want %s", got, want)
	}
}

// A pane can wrap the zone onto the next line.
func TestClaudeResetZoneOnNextLine(t *testing.T) {
	london := zone(t, "Europe/London")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, zone(t, "Asia/Tokyo"))
	if got, want := ResetTime("You've hit your 5-hour limit · resets 8:40pm\n(Europe/London)", now), time.Date(2026, 11, 4, 20, 40, 0, 0, london); !got.Equal(want) {
		t.Fatalf("%s want %s", got, want)
	}
}

// A weekly reset names a day; the year is the next one with that day ahead.
func TestClaudeWeeklyResetWithDate(t *testing.T) {
	london := zone(t, "Europe/London")
	for _, tc := range []struct {
		text      string
		now, want time.Time
	}{
		{"You've hit your weekly limit · resets Nov 10, 5pm (Europe/London)", time.Date(2026, 11, 4, 12, 0, 0, 0, london), time.Date(2026, 11, 10, 17, 0, 0, 0, london)},
		{"You've hit your weekly limit · resets Jan 2 at 9:30am (Europe/London)", time.Date(2026, 12, 30, 12, 0, 0, 0, london), time.Date(2027, 1, 2, 9, 30, 0, 0, london)},
	} {
		if got := ResetTime(tc.text, tc.now); !got.Equal(tc.want) {
			t.Fatalf("%q: %s want %s", tc.text, got, tc.want)
		}
	}
}

func TestEarlierResetFormatsStillParse(t *testing.T) {
	london := zone(t, "Europe/London")
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, london)
	if got, want := ResetTime("You've hit your 5-hour limit · resets 3am (Europe/London)", now), time.Date(2026, 11, 5, 3, 0, 0, 0, london); !got.Equal(want) {
		t.Fatalf("%s want %s", got, want)
	}
	if got, want := ResetTime("You’ve hit your usage limit. Try again at 8:48 AM.", now), time.Date(2026, 11, 5, 8, 48, 0, 0, london); !got.Equal(want) {
		t.Fatalf("%s want %s", got, want)
	}
}

// A limit with no readable reset expires by itself after a conservative wait.
func TestUnreadableResetFallsBack(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for text, wait := range map[string]time.Duration{
		"You've hit your 5-hour limit":                         5 * time.Hour,
		"You've hit your 5-hour limit · resets 3am (Bad/Zone)": 5 * time.Hour,
		"usage limit":                             time.Hour,
		"usage limit resets tomorrow":             time.Hour,
		"usage limit resets 25am (Europe/London)": time.Hour,
	} {
		if got := ResetTime(text, now); !got.Equal(now.Add(wait)) {
			t.Fatalf("%q: %s want %s", text, got, now.Add(wait))
		}
	}
}

func TestResetMarkExpires(t *testing.T) {
	t.Setenv("DUOLOG_LEAD", "")
	for _, text := range []string{"You've hit your 5-hour limit · resets 8:40pm (Europe/London)", "You've hit your 5-hour limit", "usage limit"} {
		reg, err := session.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err = reg.MarkUnavailable("claude", ResetTime(text, time.Now())); err != nil {
			t.Fatal(err)
		}
		if u, err := reg.Unavailable(); err != nil || !u["claude"] {
			t.Fatalf("%q: not marked: %v %v", text, u, err)
		}
		// Marked two days ago, the reset has passed.
		if err = reg.MarkUnavailable("claude", ResetTime(text, time.Now().Add(-48*time.Hour))); err != nil {
			t.Fatal(err)
		}
		if u, err := reg.Unavailable(); err != nil || u["claude"] {
			t.Fatalf("%q: still marked: %v %v", text, u, err)
		}
	}
}

// A line without a reset never replaces a reset read from another line.
func TestReadResetKeptOverFallback(t *testing.T) {
	s := spec(t, "claude")
	install(t, &s, testbin.Scenario{Exit: 1, Stderr: "You've hit your 5-hour limit · resets 8:40pm (Europe/London)\nrate_limit\n"})
	before := time.Now()
	r, _ := Run(context.Background(), s)
	want := ResetTime("resets 8:40pm (Europe/London)", before)
	if !r.QuotaEnded || !r.ResetAt.Equal(want) {
		t.Fatalf("%s want %s", r.ResetAt, want)
	}
}
