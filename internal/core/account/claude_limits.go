package account

import (
	"encoding/json"
	"time"
)

// ClaudeWindows reads the known unifiedWindows of a rate_limit_info object.
// An invalid known window rejects the set; unknown names carry no evidence.
func ClaudeWindows(info []byte) []Window {
	var event struct {
		Windows map[string]json.RawMessage `json:"unifiedWindows"`
	}
	if json.Unmarshal(info, &event) != nil {
		return nil
	}
	var out []Window
	for _, spec := range []struct {
		name, kind, group string
		minutes           int
	}{
		{"five_hour", "session", "session", 300},
		{"seven_day", "weekly_all", "weekly", 10080},
	} {
		raw, ok := event.Windows[spec.name]
		if !ok {
			continue
		}
		var w struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    int64    `json:"resetsAt"`
			Status      string   `json:"status"`
		}
		if json.Unmarshal(raw, &w) != nil || w.Utilization == nil || *w.Utilization < 0 || *w.Utilization > 1 || w.ResetsAt <= 0 {
			return nil
		}
		severity := "unknown"
		switch w.Status {
		case "allowed", "allowed_warning":
			severity = "normal"
		case "rejected":
			severity = "rejected"
		}
		out = append(out, Window{Kind: spec.kind, Group: spec.group, DurationMinutes: spec.minutes, UsedPercent: *w.Utilization * 100, ResetsAt: time.Unix(w.ResetsAt, 0).UTC(), Severity: severity})
	}
	return out
}
