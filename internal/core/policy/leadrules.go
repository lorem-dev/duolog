package policy

import (
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

type LeadRules struct {
	Deny []config.Rule
	Mode mode.Mode
	// RoleSandbox is supplied from the resolved role, never from hook input.
	RoleSandbox string
}
