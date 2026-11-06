package session

import (
	"fmt"
	"os"
	"path/filepath"
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
	live, err := r.liveLeads()
	if err != nil {
		return Lead{}, err
	}
	if l, ok, err := ancestorLead(live); err != nil || ok {
		return l, err
	}
	if len(live) == 1 {
		return live[0], nil
	}
	if len(live) == 0 {
		return Lead{}, fmt.Errorf("no live lead; start a lead session in this project, or run `duolog lead register` inside the lead session")
	}
	return Lead{}, fmt.Errorf("several live leads; select one with --lead or DUOLOG_LEAD")
}

// liveLeads returns the registry's leads that are still live.
func (r *Registry) liveLeads() ([]Lead, error) {
	var ids []string
	if err := r.access(false, func(s *state) error {
		for id := range s.Leads {
			ids = append(ids, id)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var live []Lead
	for _, id := range ids {
		l, ok, err := r.Lead(id)
		if err != nil {
			return nil, err
		}
		if ok {
			live = append(live, l)
		}
	}
	return live, nil
}

// AncestorLead returns the live lead of this registry whose process is the
// nearest of the caller's parent processes, and true; false when no lead of
// this registry is among them. Unlike ResolveLead it reads no flag and no
// environment, so a command cannot name a lead other than the one it runs in.
func (r *Registry) AncestorLead() (Lead, bool, error) {
	live, err := r.liveLeads()
	if err != nil {
		return Lead{}, false, err
	}
	return ancestorLead(live)
}

// ancestorLead walks the caller's parent processes for one of live.
func ancestorLead(live []Lead) (Lead, bool, error) {
	for pid, steps := os.Getpid(), 0; pid > 1 && steps < 64; steps++ {
		var matches []Lead
		for _, l := range live {
			if l.PID > 0 && l.PID == pid {
				matches = append(matches, l)
			}
		}
		if len(matches) == 1 {
			return matches[0], true, nil
		}
		if len(matches) > 1 {
			return Lead{}, false, fmt.Errorf("several ancestor leads; select one with --lead or DUOLOG_LEAD")
		}
		next, _ := parentProcess(pid)
		if next == pid {
			break
		}
		pid = next
	}
	return Lead{}, false, nil
}

// AgentAbove reports whether the caller runs inside a Claude or Codex
// session: Claude Code marks its tool shells with CLAUDECODE=1, and a claude
// or codex program is among the caller's parent processes. The person's own
// terminal has neither.
func AgentAbove(environ []string) bool {
	for _, kv := range environ {
		if kv == "CLAUDECODE=1" {
			return true
		}
	}
	for pid, steps := os.Getppid(), 0; pid > 1 && steps < 64; steps++ {
		next, name := parentProcess(pid)
		name = strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".exe")
		if name == "claude" || name == "codex" {
			return true
		}
		if next == pid || next <= 0 {
			break
		}
		pid = next
	}
	return false
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
			if len(s.Waiting[id]) == 0 {
				delete(s.Waiting, id)
			}
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
