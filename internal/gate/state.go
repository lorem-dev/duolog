package gate

import (
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
)

// LeadMode reads permissions at decision time, keeping the user's explicit
// lower ceiling. Claude's mode is recorded from each lead hook's stdin.
func LeadMode(reg *session.Registry, id string) (session.Lead, mode.Mode, error) {
	lead, effective, err := reg.EffectiveMode(id)
	if err != nil {
		return lead, effective, err
	}
	if lead.TranscriptPath != "" && lead.Agent == "codex" {
		before := lead
		observed, _ := session.ReadLeadState(lead.Agent, lead.TranscriptPath)
		live := observed.Mode
		if live == "" {
			live = mode.Lower(lead.Mode, mode.Ask)
		}
		// EffectiveMode includes the last observed mode; recompute against only the
		// explicit ceiling after refreshing the live observation.
		lead.Mode = live
		if _, err = reg.UpdateLead(before, lead); err != nil {
			return lead, "", err
		}
		return reg.EffectiveMode(id)
	}
	return lead, effective, nil
}
