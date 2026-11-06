package paidruns

import "context"

// StepHeld reports whether the step with receipt key still holds an account
// reservation: a worker that may still run, since only a proven end frees
// one. It reads the state and changes nothing.
func (s *Store) StepHeld(ctx context.Context, key string) (bool, error) {
	held := false
	err := s.View(ctx, func(st State) error {
		for _, r := range st.Reservations {
			if r.Step == key {
				held = true
			}
		}
		return nil
	})
	return held, err
}
