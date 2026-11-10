package quota

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/lorem-dev/duolog/internal/core/account"
)

// claudeProbeOrNoQuota probes only a confirmed claude.ai subscription. The
// process and timeout are the same as the read, including its route settings.
func claudeProbeOrNoQuota(ctx context.Context, run runner, usage claudeReports, s Snapshot) (Snapshot, error) {
	if len(run.p.Signals) != 0 || !claudeCanProbe(s, usage) || run.probeUsed == nil || *run.probeUsed {
		return Snapshot{Billing: s.Billing}, usage.noQuota(s.Billing)
	}
	if retained, ok := retainedClaudeQuota(ctx, s); ok {
		return retained, nil
	}
	*run.probeUsed = true
	evidence, ok := readClaudeProbe(ctx, run)
	s.Billing.OverageEnabled = s.Billing.OverageEnabled || evidence.OverageEnabled || evidence.UsingOverage
	for _, reason := range evidence.WorkspaceReasons {
		addWorkspaceReason(&s.Billing, reason)
	}
	if !ok {
		if evidence.OverageEnabled || evidence.UsingOverage || len(evidence.WorkspaceReasons) > 0 {
			return Snapshot{Billing: s.Billing}, errors.New("quota probe failed with billing evidence")
		}
		return Snapshot{Billing: s.Billing}, usage.noQuota(s.Billing)
	}
	s.Windows = evidence.Windows
	s.Rejected = slices.ContainsFunc(s.Windows, func(w Window) bool { return w.Rejected() })
	s.Source = "rate_limit_event (haiku probe)"
	return s, nil
}

func claudeCanProbe(s Snapshot, usage claudeReports) bool {
	b := s.Billing
	if b.WindowsAvailable != nil && !*b.WindowsAvailable {
		return false
	}
	if usage.reported && usage.evidence == nil {
		return false
	}
	return s.Plan != "" && b.RouteKnown && b.AuthMethod == "claude.ai" && len(b.RouteSignals) == 0
}

func readClaudeProbe(ctx context.Context, run runner) (account.RunEvidence, bool) {
	cmd := run.command(ctx, "-p", "ok", "--model", "haiku", "--output-format", "stream-json", "--verbose", "--no-session-persistence")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return account.RunEvidence{}, false
	}
	if cmd.Start() != nil {
		return account.RunEvidence{}, false
	}
	closeOnCancel := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer closeOnCancel()
	scanner := scanLines(stdout)
	var evidence account.RunEvidence
	for scanner.Scan() {
		evidence.Add(claudeProbeEvent(scanner.Bytes()))
	}
	if scanner.Err() != nil {
		_ = cmd.Process.Kill()
	}
	// A rejected quota window can make the model call exit nonzero. Keep
	// explicit window evidence independently of that model call's result.
	_ = cmd.Wait()
	return evidence, scanner.Err() == nil && len(evidence.Windows) > 0
}

func claudeProbeEvent(line []byte) account.RunEvidence {
	var event struct {
		Type string          `json:"type"`
		Info json.RawMessage `json:"rate_limit_info"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type != "rate_limit_event" {
		return account.RunEvidence{}
	}
	e, _ := account.ClaudeRateLimit(event.Info)
	return e
}
