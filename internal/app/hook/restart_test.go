package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
)

// restartMarker is the path of the marker of e's lead.
func restartMarker(e Env) string {
	return filepath.Join(e.Root, ".duolog", "restart", filepath.Base(id(e)))
}

// ageMarker sets the marker of e's lead to an hour ago and returns that time.
func ageMarker(t *testing.T, e Env) time.Time {
	t.Helper()
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(restartMarker(e), old, old); err != nil {
		t.Fatal(err)
	}
	return old
}

func markerTime(t *testing.T, e Env) time.Time {
	t.Helper()
	info, err := os.Stat(restartMarker(e))
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// A lead is told once, at the prompt after an install wrote the skills
// following its session start, and not again: the note compares with the
// marker, so only a marker that moved past the install ends it.
func TestPromptTellsLeadToRestartOnce(t *testing.T) {
	e, _, _ := setup(t, "claude")
	var installed time.Time
	e.RestartNote = func(since time.Time) string {
		if !installed.IsZero() && since.Before(installed) {
			return "restart now"
		}
		return ""
	}
	SessionStart(context.Background(), e)
	if out := Prompt(context.Background(), e); strings.Contains(string(out.Stdout), "restart now") {
		t.Fatalf("a current session is told: %s", out.Stdout)
	}
	ageMarker(t, e)
	installed = time.Now().Add(-time.Minute)
	if out := Prompt(context.Background(), e); !strings.Contains(string(out.Stdout), "restart now") {
		t.Fatalf("a stale session is not told: %s", out.Stdout)
	}
	if out := Prompt(context.Background(), e); strings.Contains(string(out.Stdout), "restart now") {
		t.Fatalf("told twice: %s", out.Stdout)
	}
}

// A lead with no marker, as one started before markers existed has, counts
// from its registered start: it is told once when that start is older than
// what install wrote, and the marker is written either way.
func TestLeadWithoutMarkerCountsFromItsRegisteredStart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed time.Duration
		told      bool
	}{
		{"started before the install", -time.Minute, true},
		{"started after the install", -2 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := setup(t, "claude")
			installed := time.Now().Add(tc.installed)
			var seen []time.Time
			e.RestartNote = func(since time.Time) string {
				seen = append(seen, since)
				if since.Before(installed) {
					return "restart now"
				}
				return ""
			}
			e.StartedAt = time.Now().Add(-time.Hour)
			SessionStart(context.Background(), e)
			e.StartedAt = time.Time{}
			if err := os.Remove(restartMarker(e)); err != nil {
				t.Fatal(err)
			}
			out := Prompt(context.Background(), e)
			if got := strings.Contains(string(out.Stdout), "restart now"); got != tc.told {
				t.Fatalf("told %v, want %v: %s", got, tc.told, out.Stdout)
			}
			if _, err := os.Stat(restartMarker(e)); err != nil {
				t.Fatalf("no marker after the prompt: %v", err)
			}
			if len(seen) == 0 || time.Since(seen[0]) < 30*time.Minute {
				t.Fatalf("compared with %v, want the registered start", seen)
			}
			if out := Prompt(context.Background(), e); strings.Contains(string(out.Stdout), "restart now") {
				t.Fatalf("told twice: %s", out.Stdout)
			}
		})
	}
}

// With no marker and no registered start nothing says when the lead started,
// so it is told nothing, and the marker is written for the next install.
func TestLeadWithoutMarkerOrRegisteredStartStaysSilent(t *testing.T) {
	e, r, _ := setup(t, "claude")
	pid := os.Getpid()
	if _, err := r.ObserveLead(session.LeadObservation{ID: id(e), Agent: "claude", PID: &pid}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.RestartNote = func(time.Time) string { return "restart now" }
	if out := Prompt(context.Background(), e); strings.Contains(string(out.Stdout), "restart now") {
		t.Fatalf("told with no start to compare: %s", out.Stdout)
	}
	if _, err := os.Stat(restartMarker(e)); err != nil {
		t.Fatalf("no marker after the prompt: %v", err)
	}
}

// countRestartLines runs five prompts and counts the ones that carry the line.
func countRestartLines(e Env) int {
	told := 0
	for range 5 {
		if out := Prompt(context.Background(), e); strings.Contains(string(out.Stdout), "restart now") {
			told++
		}
	}
	return told
}

// The line comes only with a marker that moved: one that cannot be written
// would otherwise bring it on every prompt.
func TestRestartLineDoesNotRepeatWhenTheMarkerCannotBeWritten(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block func(t *testing.T, marker string)
	}{
		{"read-only marker", func(t *testing.T, marker string) {
			if os.Geteuid() == 0 {
				t.Skip("root writes a read-only file")
			}
			if err := os.Chmod(marker, 0o400); err != nil {
				t.Fatal(err)
			}
		}},
		{"folder at the marker path", func(t *testing.T, marker string) {
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(marker, 0o750); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := setup(t, "claude")
			installed := time.Now().Add(-time.Minute)
			e.RestartNote = func(since time.Time) string {
				if since.Before(installed) {
					return "restart now"
				}
				return ""
			}
			SessionStart(context.Background(), e)
			tc.block(t, restartMarker(e))
			ageMarker(t, e)
			if told := countRestartLines(e); told != 0 {
				t.Fatalf("told %d of 5 prompts", told)
			}
		})
	}
}

// A file dated in the future stays newer than any marker until that date, and
// no install wrote it at that time, so it says nothing.
func TestRestartLineDoesNotRepeatForAFileDatedInTheFuture(t *testing.T) {
	for _, ahead := range []time.Duration{30 * time.Second, 50 * time.Second, 2 * time.Minute, 24 * time.Hour} {
		t.Run(ahead.String(), func(t *testing.T) {
			e, _, _ := setup(t, "claude")
			future := time.Now().Add(ahead)
			e.RestartNote = func(since time.Time) string {
				if future.After(since) {
					return "restart now"
				}
				return ""
			}
			SessionStart(context.Background(), e)
			if told := countRestartLines(e); told != 0 {
				t.Fatalf("told %d of 5 prompts", told)
			}
		})
	}
}

// A compaction or a clear goes on in the same process, which has not loaded
// what an install wrote, so only a start of the process moves the marker.
func TestOnlyARealStartMovesTheRestartMarker(t *testing.T) {
	for _, tc := range []struct {
		source string
		moves  bool
	}{
		{"compact", false},
		{"clear", false},
		{"startup", true},
		{"resume", true},
	} {
		t.Run(tc.source, func(t *testing.T) {
			e, _, _ := setup(t, "claude")
			e.RestartNote = func(time.Time) string { return "" }
			SessionStart(context.Background(), e)
			old := ageMarker(t, e)
			SessionStart(context.Background(), change(t, e, "source", tc.source))
			if moved := !markerTime(t, e).Equal(old); moved != tc.moves {
				t.Fatalf("marker moved %v, want %v", moved, tc.moves)
			}
		})
	}
}

// A compaction leaves the note for the next prompt.
func TestCompactionKeepsTheRestartNote(t *testing.T) {
	e, _, _ := setup(t, "claude")
	var installed time.Time
	e.RestartNote = func(since time.Time) string {
		if !installed.IsZero() && since.Before(installed) {
			return "restart now"
		}
		return ""
	}
	SessionStart(context.Background(), e)
	ageMarker(t, e)
	installed = time.Now().Add(-time.Minute)
	SessionStart(context.Background(), change(t, e, "source", "compact"))
	if out := Prompt(context.Background(), e); !strings.Contains(string(out.Stdout), "restart now") {
		t.Fatalf("the note was lost to a compaction: %s", out.Stdout)
	}
}
