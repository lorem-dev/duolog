package delegate

import (
	"fmt"
	"time"

	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/trust"
)

// observedLead brings the registered lead up to date with its transcript, and
// returns it with the mode that limits workers.
func observedLead(d Deps, reg *session.Registry, lead session.Lead, started time.Time) (session.Lead, mode.Mode, error) {
	if lead.TranscriptPath != "" {
		observed, _ := d.ReadLeadState(lead.Agent, lead.TranscriptPath)
		if observed.Model != "" {
			lead.Model = observed.Model
		}
		if observed.Effort != "" {
			lead.Effort = observed.Effort
		}
		if lead.Agent == "codex" {
			if observed.Mode != "" {
				lead.Mode = observed.Mode
			} else {
				lead.Mode = mode.Lower(lead.Mode, mode.Ask)
			}
		}
		o := session.LeadObservation{ID: lead.ID, Agent: lead.Agent, Model: &lead.Model, Effort: &lead.Effort}
		if lead.Agent == "codex" {
			o.Mode = &lead.Mode
		}
		var err error
		if lead, err = reg.ObserveLead(o, started); err != nil {
			return lead, mode.Ask, err
		}
	}
	return reg.EffectiveMode(lead.ID)
}

// trustWarning says what the lead must know about the worker's rules. A write role
// with untrusted Codex hooks would run with no gate, so it does not start.
func trustWarning(route coreroute.Route, role string, state trust.State, folder bool) (string, error) {
	warning := ""
	if route.Agent == "claude" && !state.Installed {
		warning = "; Claude has no duolog hooks: rules are off; sandbox only"
	}
	if !state.Trusted {
		if route.Sandbox != "read-only" {
			return "", fmt.Errorf("Codex hooks untrusted; open /hooks before running write role %s", role) //nolint:staticcheck // ST1005: Codex is a product name
		}
		warning = "; rules are off; open /hooks"
	}
	if !folder {
		warning += "; folder trust missing"
	}
	return warning, nil
}
