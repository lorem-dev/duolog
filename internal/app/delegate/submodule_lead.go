package delegate

import (
	"github.com/lorem-dev/duolog/internal/adapter/session"
	leadapp "github.com/lorem-dev/duolog/internal/app/lead"
)

// submoduleLead resolves the run's lead and claims the one-time reminder of a
// submodule's own rules for workRoot. The reminder comes back as the run's
// first notice, none when it was given before or workRoot is no submodule. A
// failed lookup in a submodule says which lead home was searched.
func submoduleLead(reg *session.Registry, explicit, workRoot string) (session.Lead, []string, error) {
	lead, err := reg.ResolveLead(explicit)
	if err != nil {
		return lead, nil, leadapp.SubmoduleLookupError(err, workRoot)
	}
	reminder, err := leadapp.ClaimSubmoduleReminder(reg, lead.ID, workRoot)
	if err != nil || reminder == "" {
		return lead, nil, err
	}
	return lead, []string{reminder}, nil
}
