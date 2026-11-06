package paidrules

import (
	"errors"
	"fmt"
	"math"
)

// Decision is the answer to an Admission. An admitted Reservation is in the
// state before the caller sees it. Reason names why a request was refused;
// Live and Limit are the sessions of the limit that decided, by default the
// agent's.
type Decision struct {
	Admitted    bool
	Reservation Reservation
	Reason      string
	Live, Limit int
}

// usdEpsilon absorbs float rounding, so a request that lands exactly on the
// cap is admitted.
const usdEpsilon = 1e-9

func (e Estimate) validate() error {
	if e.Input < 0 || e.Cached < 0 || e.Output < 0 || e.Cached > e.Input {
		return errors.New("estimate tokens must be 0 or more, cached at most input")
	}
	if !finiteAmount(e.USD) {
		return errors.New("the USD estimate must be a finite number, 0 or more")
	}
	for _, w := range e.Windows {
		if !finiteAmount(w.Used) || !finiteAmount(w.Reserved) || !finiteAmount(w.Cost) || !finiteAmount(w.Reserve) {
			return fmt.Errorf("window %s: shares must be finite numbers, 0 or more", w.Key.Kind)
		}
	}
	return nil
}

// finiteAmount reports whether v is a finite number, 0 or more. NaN fails
// every comparison, so it fails here too.
func finiteAmount(v float64) bool {
	return v >= 0 && v <= math.MaxFloat64
}
