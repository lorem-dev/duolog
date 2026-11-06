package policy

import "github.com/lorem-dev/duolog/internal/core/config"

// ValidateRule exposes validateRule to the lead-rule import in app/gate, which
// must refuse a deny rule that Evaluate would refuse.
func ValidateRule(r config.Rule) error { return validateRule(r) }
