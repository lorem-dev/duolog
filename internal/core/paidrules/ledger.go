package paidrules

import (
	"errors"
	"fmt"
)

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
