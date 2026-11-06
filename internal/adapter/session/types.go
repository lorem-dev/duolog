package session

import "github.com/lorem-dev/duolog/internal/core/lead"

// The data types live in core/lead; these aliases keep every caller's name.
type (
	Lead            = lead.Lead
	LeadObservation = lead.LeadObservation
	Entry           = lead.Entry
	LeadState       = lead.LeadState
	LeadRoute       = lead.LeadRoute
)
