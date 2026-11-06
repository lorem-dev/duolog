package main

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/lorem-dev/duolog/internal/calibration"
	"github.com/lorem-dev/duolog/internal/quota"
)

// quotaReader reads an agent's quota windows for calibration; tests replace
// it. This file is the only one of calibration that imports internal/quota.
var quotaReader calibration.QuotaReader = readQuota

// Window lengths in minutes that internal/quota reports.
const (
	fiveHourMinutes = 300
	weeklyMinutes   = 10080
)

// readQuota maps a quota reading onto calibration's weekly and five-hour
// windows by their length and drops the others. A rejected reading or a
// reached spend control is an error, so the run never spends blind.
func readQuota(ctx context.Context, agent string) ([]calibration.Window, error) {
	s, err := (quota.Reader{Version: version}).Read(ctx, agent)
	if err != nil {
		return nil, err
	}
	if s.Rejected || s.SpendControlReached {
		return nil, errors.New("quota rejected or spend control reached")
	}
	return quotaWindows(s), nil
}

// quotaWindows maps the account's windows, then each model family's own
// limits (Codex's per-model limits) with the family in Model, sorted by
// family; admission keeps the reserve in the ones of the cell's model too.
func quotaWindows(s quota.Snapshot) []calibration.Window {
	var ws []calibration.Window
	add := func(model string, windows []quota.Window) {
		for _, w := range windows {
			var kind string
			switch w.DurationMinutes {
			case fiveHourMinutes:
				kind = calibration.KindFiveHour
			case weeklyMinutes:
				kind = calibration.KindWeekly
			default:
				continue
			}
			ws = append(ws, calibration.Window{Kind: kind, Model: model, UsedPercent: w.UsedPercent, ResetsAt: w.ResetsAt})
		}
	}
	add("", s.Windows)
	for _, family := range slices.Sorted(maps.Keys(s.ModelLimits)) {
		add(family, s.ModelLimits[family])
	}
	return ws
}
