package main

import (
	"os"
	"time"

	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/telemetry"
)

// recordGate writes one completed ask as a gate event: the action's kind,
// the class of the rule that asked before escalation, the answer, who gave
// it and how long the worker waited, under the run id the worker's delegation
// passed as DUOLOG_RUN. The target, the reasons and any error stay out. An
// append that fails is counted in the store's health and otherwise ignored,
// so it cannot change the decision.
func recordGate(s *telemetry.Store, a gate.Action, initial gate.Decision, result gate.AskResult) {
	if s == nil {
		return
	}
	_ = s.Append(telemetry.Gate{
		Common:    telemetry.Common{Run: runID(os.Getenv("DUOLOG_RUN"))},
		Kind:      gateKind(a.Kind),
		RuleClass: gateRuleClass(initial.Rule),
		Answer:    gateAnswer(result.Answer),
		By:        gateBy(result.By),
		WaitSecs:  min(max(int64(result.Wait.Round(time.Second)/time.Second), 0), maxSecs),
	})
}

// gateKind keeps the action kinds the gate asks about.
func gateKind(kind string) string {
	switch kind {
	case "read", "write", "exec", "net", "mcp", "unknown":
		return kind
	}
	return "other"
}

// gateRuleClass names the kind of rule that asked. The gate's ask rules are
// closed names; a project's own ask rule, which names its match, is other.
func gateRuleClass(rule string) string {
	switch rule {
	case gate.RuleOutside:
		return "outside"
	case "on_unmatched":
		return "on_unmatched"
	case "mode ask":
		return "mode"
	case "mcp tool":
		return "mcp"
	// A target, a shell syntax, a search glob or a tool the gate cannot check.
	case "unverified action", "unknown tool", "read command":
		return "unverified"
	}
	return "other"
}

func gateAnswer(answer string) string {
	switch answer {
	case gate.Allow, gate.Deny, "timeout":
		return answer
	}
	return "other"
}

func gateBy(by string) string {
	switch by {
	case "person", "lead", "auto":
		return by
	}
	return "other"
}
