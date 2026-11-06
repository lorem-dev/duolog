package learn

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/config"
)

// Choice is the ladder entry picked for one delegation. Why is prior or
// learned here; delegate sets forced, person and escalation.
type Choice struct {
	Class        string
	Ladder       string
	Entry        config.LadderEntry
	Why          string
	RoleMismatch bool
}

// Choose walks the ladder of class and role, starting after the entry key
// `after` when given, and returns the first entry whose agent is available.
// RoleMismatch is set when the class does not list the role; the choice is
// still made, and access stays the role's.
func Choose(l config.Learning, class, role string, unavailable map[string]bool, after string) (Choice, bool) {
	key, entries := l.Ladder(class, role)
	if key == "" {
		return Choice{}, false
	}
	if after != "" {
		i := slices.IndexFunc(entries, func(e config.LadderEntry) bool { return e.Key() == after })
		if i < 0 {
			return Choice{}, false
		}
		entries = entries[i+1:]
	}
	why := "prior"
	if len(l.Learned[key].Order) > 0 {
		why = "learned"
	}
	for _, e := range entries {
		if unavailable[e.Agent] {
			continue
		}
		return Choice{
			Class:        class,
			Ladder:       key,
			Entry:        e,
			Why:          why,
			RoleMismatch: !slices.Contains(l.Classes[class].Roles, role),
		}, true
	}
	return Choice{}, false
}

// EscalationReasons are the signals the skill lists for leaving a ladder
// entry: no reproduction, no line references, inconsistent explanations, a
// fix that does not turn the test green, and a worker out of turns.
var EscalationReasons = []string{"no_repro", "no_lines", "inconsistent", "not_green", "out_of_turns"}
