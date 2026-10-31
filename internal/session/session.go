// Package session persists leads, reusable workers and provider availability.
package session

import (
	"encoding/json"
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
	Mode mode.Mode
}
type Entry struct {
	ID, Lead, Agent, Role, Thread, ThreadID string
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
func (r *Registry) Lookup(lead, agent, role, thread string) (*Entry, bool) {
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
	return found, err == nil && found != nil
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
func (r *Registry) Unavailable() map[string]bool {
	u := make(map[string]bool)
	_ = r.access(false, func(s *state) error {
		for agent, until := range s.Unavailable {
			if until.IsZero() || time.Now().Before(until) {
				u[agent] = true
			}
		}
		return nil
	})
	return u
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
func (r *Registry) Lead(id string) (Lead, bool) {
	var l Lead
	var ok bool
	err := r.access(false, func(s *state) error { l, ok = s.Leads[id]; return nil })
	return l, ok && err == nil
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
