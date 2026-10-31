// Package session persists leads, reusable workers and provider availability.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

type Lead struct {
	ID string
	config.Target
	Mode           mode.Mode
	TranscriptPath string
	PID            int
}
type Entry struct {
	ID, Lead, Agent, Role, Thread, ThreadID string
	BlockID                                 string
	PID                                     int
	Input                                   int64
	UpdatedAt                               time.Time
	Closed                                  bool
}
type Registry struct {
	Dir string
	// LeadID binds limit ownership when the caller selected a lead explicitly.
	LeadID string
}
type state struct {
	Entries     []Entry              `json:"entries"`
	Leads       map[string]Lead      `json:"leads"`
	Unavailable map[string]time.Time `json:"unavailable"`
	LimitOwners map[string]string    `json:"limit_owners,omitempty"`
	ModeLimits  map[string]mode.Mode `json:"mode_limits,omitempty"`
}

func Open(dir string) (*Registry, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	r := &Registry{Dir: dir}
	err := r.access(false, func(*state) error { return nil })
	return r, err
}

func (r *Registry) access(write bool, fn func(*state) error) error {
	lock := flock.New(filepath.Join(r.Dir, "sessions.lock"))
	if err := lock.Lock(); err != nil {
		_ = lock.Close()
		return err
	}
	defer lock.Close()
	path := filepath.Join(r.Dir, "sessions.json")
	s := state{Leads: make(map[string]Lead), Unavailable: make(map[string]time.Time)}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &s); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if s.Leads == nil {
		s.Leads = make(map[string]Lead)
	}
	if s.Unavailable == nil {
		s.Unavailable = make(map[string]time.Time)
	}
	if s.LimitOwners == nil {
		s.LimitOwners = make(map[string]string)
	}
	if s.ModeLimits == nil {
		s.ModeLimits = make(map[string]mode.Mode)
	}
	if err = fn(&s); err != nil || !write {
		return err
	}
	data, err = json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(r.Dir, ".sessions-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (r *Registry) Lookup(lead, agent, role, thread string) (*Entry, bool, error) {
	var found *Entry
	err := r.access(false, func(s *state) error {
		for i := len(s.Entries) - 1; i >= 0; i-- {
			x := s.Entries[i]
			if !x.Closed && x.Lead == lead && x.Agent == agent && x.Role == role && x.Thread == thread {
				found = &x
				break
			}
		}
		return nil
	})
	return found, found != nil, err
}
func (r *Registry) Record(e Entry) error {
	return r.RecordLimited(e, 0)
}

// RecordLimited retains at most limit reusable workers for this lead and agent.
// A nonpositive limit leaves the registry unbounded.
func (r *Registry) RecordLimited(e Entry, limit int) error {
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	return r.access(true, func(s *state) error {
		if limit > 0 {
			var active []int
			for i, x := range s.Entries {
				if !x.Closed && x.ID != e.ID && x.Lead == e.Lead && x.Agent == e.Agent {
					active = append(active, i)
				}
			}
			sort.Slice(active, func(i, j int) bool {
				return s.Entries[active[i]].UpdatedAt.Before(s.Entries[active[j]].UpdatedAt)
			})
			for len(active) >= limit {
				s.Entries[active[0]].Closed = true
				active = active[1:]
			}
		}
		for i, x := range s.Entries {
			if x.ID == e.ID {
				s.Entries[i] = e
				return nil
			}
		}
		s.Entries = append(s.Entries, e)
		return nil
	})
}
func (r *Registry) Close(id string) error {
	return r.access(true, func(s *state) error {
		if _, ok := s.Leads[id]; ok {
			delete(s.Leads, id)
			delete(s.ModeLimits, id)
			for agent, owner := range s.LimitOwners {
				if owner == id && s.Unavailable[agent].IsZero() {
					delete(s.Unavailable, agent)
					delete(s.LimitOwners, agent)
				}
			}
		}
		for i := range s.Entries {
			if s.Entries[i].ID == id || s.Entries[i].Lead == id {
				s.Entries[i].Closed = true
			}
		}
		return nil
	})
}
func (r *Registry) Unavailable() (map[string]bool, error) {
	u := make(map[string]bool)
	err := r.access(false, func(s *state) error {
		for agent, until := range s.Unavailable {
			if until.IsZero() || time.Now().Before(until) {
				u[agent] = true
			}
		}
		return nil
	})
	return u, err
}
func (r *Registry) MarkUnavailable(agent string, until time.Time) error {
	return r.access(true, func(s *state) error {
		s.Unavailable[agent] = until
		owner := r.LeadID
		if owner == "" {
			owner = CurrentLead(os.Environ())
		}
		s.LimitOwners[agent] = owner
		return nil
	})
}
func (r *Registry) RegisterLead(l Lead) error {
	return r.access(true, func(s *state) error { s.Leads[l.ID] = l; return nil })
}
func (r *Registry) Lead(id string) (Lead, bool, error) {
	var l Lead
	var ok bool
	err := r.access(false, func(s *state) error { l, ok = s.Leads[id]; return nil })
	if err == nil && ok && !ProcessAlive(l.PID) {
		err = r.Close(id)
		ok = false
	}
	return l, ok, err
}

// EffectiveMode reads the live lead observation and the user's lower ceiling in
// one snapshot. RegisterLead does not erase the explicit ceiling.
func (r *Registry) EffectiveMode(id string) (Lead, mode.Mode, error) {
	var lead Lead
	var effective mode.Mode
	err := r.access(false, func(s *state) error {
		var ok bool
		lead, ok = s.Leads[id]
		if !ok {
			return fmt.Errorf("lead %q is not registered", id)
		}
		effective = mode.Lower(lead.Mode, lead.Mode)
		if ceiling, ok := s.ModeLimits[id]; ok {
			effective = mode.Lower(effective, ceiling)
		}
		return nil
	})
	if err == nil && !ProcessAlive(lead.PID) {
		if closeErr := r.Close(id); closeErr != nil {
			return lead, "", closeErr
		}
		return lead, "", fmt.Errorf("lead %q has ended", id)
	}
	return lead, effective, err
}

func (r *Registry) SetMode(id string, requested mode.Mode) error {
	switch requested {
	case mode.Plan, mode.Ask, mode.Edits, mode.Full:
	default:
		return fmt.Errorf("unknown mode %q", requested)
	}
	return r.access(true, func(s *state) error {
		lead, ok := s.Leads[id]
		if !ok {
			return fmt.Errorf("lead %q is not registered", id)
		}
		if mode.Lower(lead.Mode, requested) != requested {
			return fmt.Errorf("cannot set mode %s above lead mode %s", requested, lead.Mode)
		}
		s.ModeLimits[id] = requested
		return nil
	})
}
func CurrentLead(env []string) string {
	for _, v := range env {
		if id, ok := strings.CutPrefix(v, "DUOLOG_LEAD="); ok {
			return id
		}
	}
	return ""
}
func Address(l Lead) string {
	id := l.ID
	if len(id) > 6 {
		id = id[:6]
	}
	return l.Agent + "/lead." + id
}

// ClearUnavailable clears a provider limit only after a successful health probe.
func (r *Registry) ClearUnavailable(agent string) error {
	return r.access(true, func(s *state) error {
		delete(s.Unavailable, agent)
		delete(s.LimitOwners, agent)
		return nil
	})
}

// Entries returns a snapshot including closed entries so surviving panes can be
// associated with their leads after Close has marked their entries closed.
func (r *Registry) Entries() ([]Entry, error) {
	var entries []Entry
	err := r.access(false, func(s *state) error {
		entries = append(entries, s.Entries...)
		return nil
	})
	return entries, err
}
