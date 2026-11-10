package install

import (
	"strings"
	"testing"
)

func TestSkillRunsRequiredReviewWithoutAsking(t *testing.T) {
	// Kills the mutation that makes required review an optional next step.
	for _, agent := range []string{"claude", "codex"} {
		text := strings.Join(strings.Fields(skillText(t, agent)), " ")
		for _, rule := range []string{
			"Finish with `review_final` (`review_cross` for big batches) without asking",
			"check worker results first",
		} {
			if !strings.Contains(text, rule) {
				t.Errorf("%s skill lacks %q", agent, rule)
			}
		}
	}
}

func TestSkillHandlesReviewFindingsBeforeEndingTurn(t *testing.T) {
	// Kills the mutation that stops after review instead of addressing findings.
	for _, agent := range []string{"claude", "codex"} {
		text := strings.Join(strings.Fields(skillText(t, agent)), " ")
		if !strings.Contains(text, "Return findings to the worker or fix them.") {
			t.Errorf("%s skill must address review findings", agent)
		}
	}
}

func TestSkillEndsTurnWithResultOrPersonDecision(t *testing.T) {
	// Kills mutations that allow idle permission questions or hide the decision.
	for _, agent := range []string{"claude", "codex"} {
		text := strings.Join(strings.Fields(skillText(t, agent)), " ")
		for _, rule := range []string{
			"End only with results or decisions only the person can make.",
			"Start closing questions with the decision.",
		} {
			if !strings.Contains(text, rule) {
				t.Errorf("%s skill lacks %q", agent, rule)
			}
		}
	}
}
