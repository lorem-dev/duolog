package paidruns

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A refusal for window headroom names the window and how much of it is used,
// not only "no window headroom", and does not print session counts that read
// like a Rex pane limit.
func TestWindowRefusalNamesTheWindow(t *testing.T) {
	w := newGateWorld(t)
	w.used = 95
	r := w.refusal(w.request("codex", workerenv.Worker, workerenv.Free, 900), ReasonWindow)
	for _, want := range []string{"codex 5h window: 95% used", "this task", "reserve", "over 100%"} {
		if !strings.Contains(r.Detail, want) {
			t.Fatalf("detail %q lacks %q", r.Detail, want)
		}
	}
	if line := r.Error(); strings.Contains(line, "sessions") {
		t.Fatalf("a window refusal prints session counts: %s", line)
	}
}

func TestWindowShortfallNamesTheFullWindow(t *testing.T) {
	costs := []paidrules.WindowCost{
		{Key: paidrules.WindowKey{Kind: "session"}, Used: 0.1, Cost: 0.05, Reserve: 0.10},
		{Key: paidrules.WindowKey{Kind: "weekly_all"}, Used: 0.85, Cost: 0.01, Reserve: 0.15},
	}
	got := paidrules.WindowShortfall("codex", costs, 1)
	if want := "codex weekly window: 85% used + 1% this task + 15% reserve = 101%, over 100%"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The line shows each part of the sum, so a large estimate reads as the
// task's cost, not as a full window.
func TestWindowShortfallShowsTheTaskCost(t *testing.T) {
	five := paidrules.WindowKey{Kind: "session"}
	got := paidrules.WindowShortfall("codex", []paidrules.WindowCost{
		{Key: five, Used: 0.09, Reserved: 0.2, Cost: 0.9, Reserve: 0.10},
	}, 1)
	if want := "codex 5h window: 9% used + 20% held by running tasks + 90% this task + 10% reserve = 129%, over 100%"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// A window that fits by these costs: tasks in flight they do not count
	// took the rest.
	got = paidrules.WindowShortfall("codex", []paidrules.WindowCost{{Key: five, Used: 0.5, Cost: 0.05, Reserve: 0.10}}, 1)
	if want := "codex 5h window: 50% used + 5% this task + 10% reserve = 65%; running tasks hold the rest"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
