package session

import (
	"fmt"
	"os"
	"strings"
)

// ResolveLead selects an explicit ID, environment ID, nearest ancestor lead,
// then the sole live project lead. Ambiguity never starts an unowned worker.
func (r *Registry) ResolveLead(explicit string) (Lead, error) {
	id := explicit
	if id == "" {
		id = CurrentLead(os.Environ())
	}
	if id != "" {
		if strings.Contains(id, "/lead.") {
			l, ok, err := r.LeadByAddress(id)
			if err != nil {
				return Lead{}, err
			}
			if ok {
				return l, nil
			}
		}
		l, ok, err := r.Lead(id)
		if err != nil {
			return Lead{}, err
		}
		if !ok {
			return Lead{}, fmt.Errorf("lead %q is unavailable; start a lead session", id)
		}
		return l, nil
	}
	var ids []string
	if err := r.access(false, func(s *state) error {
		for id := range s.Leads {
			ids = append(ids, id)
		}
		return nil
	}); err != nil {
		return Lead{}, err
	}
	var live []Lead
	for _, id := range ids {
		l, ok, err := r.Lead(id)
		if err != nil {
			return Lead{}, err
		}
		if ok {
			live = append(live, l)
		}
	}
	for pid, steps := os.Getpid(), 0; pid > 1 && steps < 64; steps++ {
		var matches []Lead
		for _, l := range live {
			if l.PID > 0 && l.PID == pid {
				matches = append(matches, l)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return Lead{}, fmt.Errorf("several ancestor leads; select one with --lead or DUOLOG_LEAD")
		}
		next, _ := parentProcess(pid)
		if next == pid {
			break
		}
		pid = next
	}
	if len(live) == 1 {
		return live[0], nil
	}
	if len(live) == 0 {
		return Lead{}, fmt.Errorf("no live lead; start a lead session before using duolog")
	}
	return Lead{}, fmt.Errorf("several live leads; select one with --lead or DUOLOG_LEAD")
}

// Waiting records each active foreground delegate separately, so overlapping
// runs cannot clear each other's wait marker. Dead callers do not block asks.
func (r *Registry) SetWaiting(id, token string, pid int) error {
	return r.access(true, func(s *state) error {
		if s.Waiting == nil {
			s.Waiting = map[string]map[string]int{}
		}
		if s.Waiting[id] == nil {
			s.Waiting[id] = map[string]int{}
		}
		if pid == 0 {
			delete(s.Waiting[id], token)
		} else {
			s.Waiting[id][token] = pid
		}
		return nil
	})
}
func (r *Registry) IsWaiting(id string) (bool, error) {
	waiting := false
	err := r.access(false, func(s *state) error {
		for _, pid := range s.Waiting[id] {
			waiting = waiting || ProcessAlive(pid)
		}
		return nil
	})
	return waiting, err
}
