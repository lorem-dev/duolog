package session

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// SessionEnvNames are the variables in which the registered agents name the
// session a command runs in, as their descriptors' SessionEnv list them, in
// order. ResolveLead reads them as lead hints. The wiring package sets them;
// unset, the environment gives no hints.
var SessionEnvNames []string

// ResolveLead selects an explicit ID, then DUOLOG_LEAD. Then the first of
// hints, and after them the session IDs that SessionEnvNames name in the
// environment, that is a live lead; a hint that is none, as a worker's own
// session ID, is no error. Then the nearest ancestor lead, with the hints
// choosing among leads that share a process, as a Codex app-server's threads
// do, and then the sole live project lead. Ambiguity never starts an unowned
// worker.
func (r *Registry) ResolveLead(explicit string, hints ...string) (Lead, error) {
	id := explicit
	if id == "" {
		id = CurrentLead(os.Environ())
	}
	if id != "" {
		return r.namedLead(id)
	}
	hints = append(slices.Clip(hints), EnvHints(os.Environ())...)
	if l, ok, err := r.hintedLead(hints); err != nil || ok {
		return l, err
	}
	live, err := r.liveLeads()
	if err != nil {
		return Lead{}, err
	}
	if l, ok, err := ancestorLead(live, hints...); err != nil || ok {
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

// namedLead is the live lead that id names, by session ID or bus address; a
// lead that is not live is an error.
func (r *Registry) namedLead(id string) (Lead, error) {
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
		return Lead{}, fmt.Errorf("lead %q is not a live lead in %s: it was never registered in this project's state, or its session ended; run `duolog lead register` inside the lead session", id, r.Dir)
	}
	return l, nil
}

// hintedLead is the live lead that the first of hints names, and true; false
// when none of them names one.
func (r *Registry) hintedLead(hints []string) (Lead, bool, error) {
	for _, id := range hints {
		if id == "" {
			continue
		}
		l, ok, err := r.Lead(id)
		if err != nil || ok {
			return l, ok, err
		}
	}
	return Lead{}, false, nil
}

// revive drops the tombstone of o's lead, which a later observation brings
// back, and ends the leads o's process no longer runs (endProcessPeers).
func (s *state) revive(o LeadObservation, started time.Time) {
	delete(s.Ended, o.ID)
	s.endProcessPeers(o, started)
}

// endProcessPeers ends, for an observation with SoleInProcess and a PID, the
// other live leads of the same agent that run in that process: their
// process now runs o's session, as after a /clear. An observation without
// them ends nothing, so leads that share a process, as Codex threads do,
// stay.
func (s *state) endProcessPeers(o LeadObservation, started time.Time) {
	if !o.SoleInProcess || o.PID == nil || *o.PID <= 0 {
		return
	}
	for id, l := range s.Leads {
		if id != o.ID && l.Agent == o.Agent && l.PID == *o.PID {
			_, _ = s.observeLead(LeadObservation{ID: id, Agent: l.Agent, Ended: true}, started)
		}
	}
}

// EnvHints returns the session IDs that environ sets under SessionEnvNames,
// in their order and each once.
func EnvHints(environ []string) []string {
	var hints []string
	for _, name := range SessionEnvNames {
		for _, kv := range environ {
			if v, ok := strings.CutPrefix(kv, name+"="); ok && v != "" && !slices.Contains(hints, v) {
				hints = append(hints, v)
			}
		}
	}
	return hints
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
