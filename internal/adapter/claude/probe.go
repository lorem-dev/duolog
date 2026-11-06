package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// Probe checks Claude for doctor: its model call and its metadata commands.
type Probe struct{}

// probeModel is the model the probe calls.
const probeModel = "haiku"

// Model returns the model the probe calls; Claude has no catalog to read.
func (Probe) Model(*config.Config, []route.Entry) (string, error) { return probeModel, nil }

// Metadata runs the claude binary with args.
func (Probe) Metadata(ctx context.Context, args ...string) ([]byte, error) {
	return kit.Metadata(ctx, Facts.Binaries[0], args...)
}

// Probe makes one model call, the empty-call form from the live verification
// bench, in plan mode, and checks that the answer is "ok".
func (Probe) Probe(ctx context.Context, _ *config.Config, p corelaunch.Process, dir, model string, started func(pid int) error) (usage.Probed, error) {
	args := []string{"-p", "--model", model, "--effort", "low", "--no-session-persistence", "--output-format", "json", "--strict-mcp-config", "--permission-mode", "plan", "--", "Reply with the word ok."}
	output, err := kit.RunProbe(ctx, p, dir, args, started)
	measured := readProbeUsage(output)
	if err != nil {
		return measured, err
	}
	var result struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return measured, err
	}
	if result.IsError || strings.TrimSpace(result.Result) != "ok" {
		return measured, fmt.Errorf("missing expected answer")
	}
	return measured, nil
}

// readProbeUsage reads the usage of a probe's output: Claude's result object
// with its cost.
func readProbeUsage(output []byte) usage.Probed {
	var u usage.Probed
	var result struct {
		Usage *struct {
			Input    int64 `json:"input_tokens"`
			Cached   int64 `json:"cache_read_input_tokens"`
			Creation int64 `json:"cache_creation_input_tokens"`
			Output   int64 `json:"output_tokens"`
			Split    *struct {
				Hour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
		Cost *float64 `json:"total_cost_usd"`
	}
	if json.Unmarshal(output, &result) != nil || result.Usage == nil {
		return u
	}
	x := result.Usage
	if x.Input < 0 || x.Cached < 0 || x.Creation < 0 || x.Output < 0 {
		return u
	}
	// Writes without a split count as five-minute writes, as the runner
	// counts them.
	u = usage.Probed{Tokens: usage.Tokens{Input: x.Input + x.Cached + x.Creation, Cached: x.Cached, Write: x.Creation,
		Output: x.Output}, Known: true}
	if x.Split != nil {
		u.WriteHour = min(max(x.Split.Hour, 0), x.Creation)
	}
	if c := result.Cost; c != nil && *c >= 0 && *c <= 1e9 {
		u.CostUSD = c
	}
	return u
}
