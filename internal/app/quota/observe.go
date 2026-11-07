package quota

import (
	"math"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/record"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
)

// Observation maps a snapshot duolog quota read to what balance would
// observe: windows by kind and family, usage in basis points, no plan name
// outside the known ones, and no credit balance, identity or vendor id.
func Observation(agent string, s account.Snapshot) record.QuotaObservation {
	q := record.QuotaObservation{Agent: agent, Source: "app_server", Plan: s.Plan, SpendControl: s.SpendControlReached}
	if agent == "claude" {
		q.Source = "usage_read"
	}
	q.Overage = (s.ExtraUsage != nil && s.ExtraUsage.Enabled) || (s.Credits != nil && (s.Credits.HasCredits || s.Credits.Unlimited))
	for _, w := range s.Windows {
		q.Windows = append(q.Windows, windowObservation(agent, "", w))
	}
	families := make([]string, 0, len(s.ModelLimits))
	for family := range s.ModelLimits {
		families = append(families, family)
	}
	sort.Strings(families)
	for _, family := range families {
		for _, w := range s.ModelLimits[family] {
			q.Windows = append(q.Windows, windowObservation(agent, family, w))
		}
	}
	return q
}

// windowObservation names a window by its length (five_hour, weekly or other)
// and the family it limits: all for a window of every model, the limit's
// family, or the family of its kind or model.
func windowObservation(agent, family string, w account.Window) record.WindowObservation {
	o := record.WindowObservation{Kind: "other", Family: family, Status: "unknown", LengthMinutes: int64(w.DurationMinutes), ResetAt: w.ResetsAt}
	switch w.DurationMinutes {
	case 300:
		o.Kind = "five_hour"
	case 10080:
		o.Kind = "weekly"
	}
	if o.Family == "" {
		kindFamily, scoped := strings.CutPrefix(w.Kind, "weekly_")
		switch {
		case w.Model != "":
			o.Family = coreroute.Normalize(agent, w.Model)
		case scoped && kindFamily != "all" && kindFamily != "scoped":
			o.Family = kindFamily
		case w.Kind == "other" || w.Kind == "weekly_scoped":
			o.Family = "other"
		default:
			o.Family = "all"
		}
	}
	// The comparisons also refuse NaN.
	if p := math.Round(w.UsedPercent * 100); p >= 0 {
		o.UsedBasisPoints = int64(min(p, 10_000))
	}
	switch w.Severity {
	case "rejected", "blocked", "exceeded":
		o.Status = "blocked"
	case "", "normal", "warning", "allowed", "allowed_warning":
		o.Status = "ok"
	}
	if w.UsedPercent >= 100 {
		o.Status = "blocked"
	}
	return o
}
