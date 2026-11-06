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
// Several leads share a process when a /clear or /resume registered a new
// session in it and the old one never ended: then the first of prefer, by ID
// or bus address, that is among them is the one, and it is an error when
// none is. A Claude or Codex process met on the way, before the lead's own,
// makes the caller a worker or peer below the lead, which is an error: only
// the lead itself answers for it.
func (r *Registry) AncestorLead(prefer ...string) (Lead, bool, error) {
	live, err := r.liveLeads()
	if err != nil {
		return Lead{}, false, err
	}
	return ancestorLeadFrom(os.Getpid(), parentProcess, live, prefer...)
}

// ancestorLeadFrom is AncestorLead's walk from the process start, with
// parent giving each process's parent and name.
func ancestorLeadFrom(start int, parent func(int) (int, string), live []Lead, prefer ...string) (Lead, bool, error) {
	return walkLeads(start, parent, live, true, prefer...)
}

// ancestorLead walks the caller's parent processes for one of live.
func ancestorLead(live []Lead, prefer ...string) (Lead, bool, error) {
	return walkLeads(os.Getpid(), parentProcess, live, false, prefer...)
}

// walkLeads walks from start up the parent processes for one of live. With
// strict, an agent process met before the lead's own is an error.
func walkLeads(start int, parent func(int) (int, string), live []Lead, strict bool, prefer ...string) (Lead, bool, error) {
	agent := ""
	for pid, steps := start, 0; pid > 1 && steps < 64; steps++ {
		if matches := leadsOf(live, pid); len(matches) > 0 {
			return pickLead(matches, agent, prefer)
		}
		next, name := parent(pid)
		if strict && agent == "" && !launcher(name) {
			agent = agentName(name)
		}
		if next == pid {
			break
		}
		pid = next
	}
	return Lead{}, false, nil
}

// leadsOf returns the leads of live whose process is pid.
func leadsOf(live []Lead, pid int) []Lead {
	var matches []Lead
	for _, l := range live {
		if l.PID > 0 && l.PID == pid {
			matches = append(matches, l)
		}
	}
	return matches
}

// pickLead chooses among matches, the leads of the nearest process that has
// any: the only one, else the first of prefer among them. agent names the
// agent process met below them, which refuses the caller.
func pickLead(matches []Lead, agent string, prefer []string) (Lead, bool, error) {
	if agent != "" {
		return Lead{}, false, fmt.Errorf("this command runs below lead %q through another %s process, a worker or peer; only the lead itself answers its asks", matches[0].ID, agent)
	}
	if len(matches) == 1 {
		return matches[0], true, nil
	}
	for _, want := range prefer {
		for _, l := range matches {
			if want != "" && (l.ID == want || Address(l) == want) {
				return l, true, nil
			}
		}
	}
	return Lead{}, false, fmt.Errorf("several ancestor leads; select one with --lead or DUOLOG_LEAD")
}

// AgentAbove reports whether the caller runs inside a Claude or Codex
// session: Claude Code marks its tool shells with CLAUDECODE=1, and a Claude
// Code or Codex process is among the caller's parent processes, as
// AgentAncestor finds one, codex-<target triple> included. The person's own
// terminal has neither.
func AgentAbove(environ []string) bool { return agentAbove(environ, os.Getppid(), parentProcess) }

func agentAbove(environ []string, start int, parent func(int) (int, string)) bool {
	for _, kv := range environ {
		if kv == "CLAUDECODE=1" {
			return true
		}
	}
	return agentAncestor(start, parent) != ""
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
