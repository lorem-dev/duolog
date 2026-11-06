package quota

import "github.com/lorem-dev/duolog/internal/core/account"

// RunEvidence is what a run told about its own billing; core/account holds it,
// because the adapters that read a run's stream fill it.
type RunEvidence = account.RunEvidence

// SafeWorkspaceReason reports whether category is one of the workspace
// evidence categories, the only reasons duolog keeps or shows.
func SafeWorkspaceReason(category string) bool {
	return account.SafeWorkspaceReason(category)
}
