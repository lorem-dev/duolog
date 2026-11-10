package claude

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/lorem-dev/duolog/internal/core/account"
)

// claudeRateLimit reads billing evidence and windows from a stream event.
func claudeRateLimit(info json.RawMessage) (account.RunEvidence, bool) {
	return account.ClaudeRateLimit(info)
}

// reportedUSD reads the total_cost_usd of Claude's result: a finite number of
// zero or more. Anything else is no reported cost, which differs from a
// reported zero.
func reportedUSD(raw json.RawMessage) *float64 {
	// json.Number would also take a number in a string, which is the wrong type.
	var number json.Number
	if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &number) != nil {
		return nil
	}
	cost, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsInf(cost, 0) || math.IsNaN(cost) || cost < 0 {
		return nil
	}
	return &cost
}
