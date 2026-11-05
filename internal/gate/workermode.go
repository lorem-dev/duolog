package gate

import "github.com/lorem-dev/duolog/internal/mode"

// WorkerMode applies the level a delegate gave its worker in DUOLOG_MODE.
// The value can only lower the lead's mode: an empty value keeps the lead's,
// and an unknown one counts as ask, so a forged value never raises the gate.
func WorkerMode(lead mode.Mode, env string) mode.Mode {
	if env == "" {
		return lead
	}
	return mode.Lower(lead, mode.Mode(env))
}
