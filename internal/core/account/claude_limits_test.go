package account

import (
	"testing"
	"time"
)

// Kills dropping or misreading weekly utilization, kind, duration or reset.
func TestClaudeUnifiedWindows(t *testing.T) {
	info := []byte(`{"status":"allowed","resetsAt":1794744000,"rateLimitType":"seven_day","overageStatus":"allowed","overageResetsAt":1796083200,"isUsingOverage":false,"unifiedWindows":{"seven_day":{"utilization":0.09,"resetsAt":1794744000,"status":"allowed"}}}`)
	ws := ClaudeWindows(info)
	if len(ws) != 1 || ws[0].Kind != "weekly_all" || ws[0].UsedPercent != 9 || !ws[0].ResetsAt.Equal(time.Unix(1794744000, 0)) || ws[0].DurationMinutes != 10080 {
		t.Fatalf("windows: %+v", ws)
	}
}

// Kills substituting the top-level reset or either sibling window's reset.
func TestClaudeUnifiedWindowsDistinctResets(t *testing.T) {
	ws := ClaudeWindows([]byte(`{"resetsAt":1794744000,"unifiedWindows":{"five_hour":{"utilization":0.2,"resetsAt":1794747600,"status":"allowed"},"seven_day":{"utilization":0.09,"resetsAt":1794830400,"status":"allowed"}}}`))
	if len(ws) != 2 || ws[0].Kind != "session" || ws[1].Kind != "weekly_all" ||
		!ws[0].ResetsAt.Equal(time.Unix(1794747600, 0)) || !ws[1].ResetsAt.Equal(time.Unix(1794830400, 0)) {
		t.Fatalf("windows: %+v", ws)
	}
}

// Kills accepting unknown windows, dropping rejection, and accepting malformed known windows.
func TestClaudeUnifiedWindowsValidation(t *testing.T) {
	ws := ClaudeWindows([]byte(`{"unifiedWindows":{"five_hour":{"utilization":1,"resetsAt":1794744000,"status":"rejected"},"unknown":{"utilization":0.1}}}`))
	if len(ws) != 1 || ws[0].Kind != "session" || !ws[0].Rejected() || ws[0].UsedPercent != 100 {
		t.Fatalf("windows: %+v", ws)
	}
	for _, value := range []string{`null`, `"0.1"`, `-0.1`, `1.1`} {
		ws = ClaudeWindows([]byte(`{"unifiedWindows":{"seven_day":{"utilization":` + value + `,"resetsAt":1794744000,"status":"allowed"}}}`))
		if len(ws) != 0 {
			t.Fatalf("accepted %s: %+v", value, ws)
		}
	}
}
