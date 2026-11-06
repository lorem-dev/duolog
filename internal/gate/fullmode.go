package gate

import "github.com/lorem-dev/duolog/internal/mode"

// confirmInFull lets full mode confirm a rule's ask. An ask that needs the
// person stays one: paths outside the work root ask in every mode.
func confirmInFull(m mode.Mode, d Decision) Decision {
	if m != mode.Full || d.Kind != Ask || PersonOnly(d) {
		return d
	}
	return Decision{Kind: Allow, Reason: "full mode confirms action", Rule: "mode full: " + d.Rule, Mode: d.Mode}
}
