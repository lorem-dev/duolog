package hook

import (
	"errors"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
)

// configError is a config that did not load while a hook prepared the lead.
type configError struct{ err error }

func (e *configError) Error() string { return e.err.Error() }
func (e *configError) Unwrap() error { return e.err }

// configLine is the one line of context that the lead's session start and
// prompt hooks add when the config does not load: the gate cannot read its
// rules then, and without the line every duolog call would fail unexplained.
// The gate keeps its own behaviour; this only tells the lead.
func configLine(event string, err error) []byte {
	var ce *configError
	if (event != "SessionStart" && event != "UserPromptSubmit") || !errors.As(err, &ce) {
		return nil
	}
	return []byte("duolog: " + configfile.Problem(ce.err) + "; the gate cannot load its rules\n")
}
