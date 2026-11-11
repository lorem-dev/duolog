package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DelegationLocation says which work project holds the record of one MCP
// delegation: WorkRoot is that project's main checkout, whose
// .duolog/delegations has the record. It is registry metadata, not a copy of
// the result, and stays after the lead ends while its record exists.
type DelegationLocation struct {
	ID, Lead, WorkRoot string
}

// RecordDelegation stores loc under its run id before the run starts. The
// same location again is no change; another lead or work root for a known id
// is an error, so one id never names two runs.
func (r *Registry) RecordDelegation(loc DelegationLocation) error {
	if loc.ID == "" || loc.Lead == "" || loc.WorkRoot == "" {
		return errors.New("delegation location needs an id, a lead and a work root")
	}
	return r.access(true, func(s *state) error {
		if have, ok := s.Delegations[loc.ID]; ok {
			if have != loc {
				return fmt.Errorf("delegation %s is already recorded for lead %s in %s", loc.ID, have.Lead, have.WorkRoot)
			}
			return nil
		}
		if s.Delegations == nil {
			s.Delegations = make(map[string]DelegationLocation)
		}
		s.Delegations[loc.ID] = loc
		return nil
	})
}

// Delegations returns the recorded locations of lead's delegations, or of
// every lead's when lead is empty, sorted by id.
func (r *Registry) Delegations(lead string) ([]DelegationLocation, error) {
	var all []DelegationLocation
	err := r.access(false, func(s *state) error {
		for _, loc := range s.Delegations {
			all = append(all, loc)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out, missing []DelegationLocation
	for _, loc := range all {
		if missingDelegation(loc) {
			missing = append(missing, loc)
		} else if lead == "" || loc.Lead == lead {
			out = append(out, loc)
		}
	}
	if len(missing) > 0 {
		err = r.access(true, func(s *state) error {
			for _, loc := range missing {
				if s.Delegations[loc.ID] == loc && missingDelegation(loc) {
					delete(s.Delegations, loc.ID)
				}
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

func missingDelegation(loc DelegationLocation) bool {
	_, err := os.Stat(filepath.Join(loc.WorkRoot, ".duolog", "delegations", loc.ID+".json"))
	return os.IsNotExist(err)
}
