package claude

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/run"
)

const weeklyStreamEvent = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1794744000,"rateLimitType":"seven_day","overageStatus":"allowed","overageResetsAt":1796083200,"isUsingOverage":false,"unifiedWindows":{"seven_day":{"utilization":0.09,"resetsAt":1794744000,"status":"allowed"}}}}`

// Kills dropping quota windows while folding billing events, including a later event with no windows.
func TestStreamKeepsWeeklyWindows(t *testing.T) {
	var result run.Result
	_, _ = readClaudeStream([]byte(weeklyStreamEvent+"\n"+`{"type":"rate_limit_event","rate_limit_info":{"isUsingOverage":true}}
`), &result)
	if len(result.Billing.Windows) != 1 || result.Billing.Windows[0].UsedPercent != 9 || !result.Billing.UsingOverage || !result.Billing.OverageEnabled {
		t.Fatalf("evidence %+v", result.Billing)
	}
}
