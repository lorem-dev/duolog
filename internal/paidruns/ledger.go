package paidruns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

// capSpan is how far back spend counts against the cap: the last 24 hours,
// never a calendar day, so no time zone matters and no midnight resets it.
const capSpan = 24 * time.Hour

// Settle records the measured end of reservation in.ID and replaces its
// estimate with the charge, once: a second settlement of the same ID changes
// nothing. The Store computes the charge; in.ChargedUSD is ignored.
//
//   - A paid task, or a free one whose run evidence shows overage or fast
//     mode, is charged the larger of the token-priced and the CLI-reported
//     cost; with incomplete usage, at least its estimate.
//   - Any other free task is charged nothing: the CLI prices subscription
//     runs too.
//
// A charge past the cap is recorded like any other. It stops nothing; the
// next admission sees it. A peer's session stays held after its task
// settles; a worker's session ends with it. Invalid amounts are errors, and
// nothing is written.
func (s *Store) Settle(ctx context.Context, in Settlement) error {
	if err := in.validate(); err != nil {
		return err
	}
	return s.Update(ctx, func(st *State) error {
		if _, ok := st.Settled[in.ID]; ok {
			return nil
		}
		r, ok := st.Reservations[in.ID]
		if !ok || !r.Task {
			return fmt.Errorf("paid runs: no task %s to settle", in.ID)
		}
		if in.Agent != "" && in.Agent != r.Agent {
			return fmt.Errorf("paid runs: task %s is of %s, not %s", in.ID, r.Agent, in.Agent)
		}
		in.Agent, in.AccountKey = r.Agent, r.AccountKey
		in.Started = in.Started || r.Started
		in.ChargedUSD = charge(r, in)
		if in.Ended.IsZero() {
			in.Ended = s.now()
		}
		st.Settled[in.ID] = in
		if r.Shape == workerenv.Peer && r.Session {
			r.Task, r.Estimate = false, Estimate{}
			st.Reservations[in.ID] = r
		} else {
			delete(st.Reservations, in.ID)
		}
		return nil
	})
}

// charge is the money a settled task spent.
func charge(r Reservation, in Settlement) float64 {
	if !r.Paid && !in.Evidence.UsingOverage && !in.Evidence.Fast {
		return 0
	}
	usd := in.PricedUSD
	if in.ReportedUSD != nil {
		usd = max(usd, *in.ReportedUSD)
	}
	if !in.CompleteUsage {
		usd = max(usd, r.Estimate.USD)
	}
	return usd
}

func (in Settlement) validate() error {
	var errs []error
	if in.ID == "" {
		errs = append(errs, errors.New("a settlement needs an id"))
	}
	if in.Input < 0 || in.Cached < 0 || in.Output < 0 || in.Cached > in.Input {
		errs = append(errs, errors.New("tokens must be 0 or more, cached at most input"))
	}
	if !finiteAmount(in.PricedUSD) || in.ReportedUSD != nil && !finiteAmount(*in.ReportedUSD) {
		errs = append(errs, errors.New("costs must be finite numbers of USD, 0 or more"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("paid runs: settlement refused: %w", err)
	}
	return nil
}

// Release drops reservation id when it is proven never to have started: no
// child was attached. That refunds its estimate and frees its slot. An ID
// that is not held, released or settled before, is nothing to release. A
// started reservation is refused: only Settle ends it, so a live child is
// never freed.
func (s *Store) Release(ctx context.Context, id string) error {
	return s.Update(ctx, func(st *State) error {
		r, ok := st.Reservations[id]
		if !ok {
			return nil
		}
		if r.Started || r.Child != (Owner{}) {
			return fmt.Errorf("paid runs: reservation %s has started; settle it", id)
		}
		delete(st.Reservations, id)
		return nil
	})
}

// Spent returns agent's spend at now: the charges settled in the 24 hours
// before now, and the estimates of its paid tasks in flight, however old.
// It counts every project and account identity of the agent.
func (s *Store) Spent(ctx context.Context, agent string, now time.Time) (settled, inflight float64, err error) {
	err = s.Update(ctx, func(st *State) error {
		settled, inflight = st.spent(agent, now)
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return settled, inflight, nil
}

func (st *State) spent(agent string, now time.Time) (settled, inflight float64) {
	for _, in := range st.Settled {
		if in.Agent == agent && now.Sub(in.Ended) < capSpan {
			settled += in.ChargedUSD
		}
	}
	for _, r := range st.Reservations {
		if r.Agent == agent && r.Task && r.Paid {
			inflight += r.Estimate.USD
		}
	}
	return settled, inflight
}
