// Package record holds the plain records that duolog writes and passes
// between packages: attempts, balance observations and journal records.
package record

import (
	"time"

	"github.com/lorem-dev/duolog/internal/core/usage"
)

// Attempt is one actual run of a task: a worker process, a peer turn, or a
// run here task the lead finished. Family is the model family, never a
// version. It carries no text from the vendor; the caller still checks every
// field before it keeps one. Run is the delegation's own random id, the one
// its workers get as DUOLOG_RUN; it is empty for a run here task.
type Attempt struct {
	Number                                        int
	Run                                           string
	Role, Class, Agent, Family, Effort, Placement string
	Usage                                         usage.Tokens
	UsageKnown                                    bool
	Duration                                      time.Duration
	Outcome, ErrorCode, Reason                    string
	Asks, Denials                                 int64
	Escalated, Moved                              bool
}

// Observer receives every attempt, learning on or off. It cannot fail a
// delegation: it returns nothing, and Run neither waits for nor reads what it
// does.
type Observer func(Attempt)
