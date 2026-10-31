// Package session persists leads, reusable workers and provider availability.
package session

import (
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/store"
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

// LeadObservation contains only fields actually read by a caller. Nil means
// unobserved; a pointer to an empty string records an explicitly unknown value.
// Callers capture the time before reading stdin, the registry or a transcript.
type LeadObservation struct {
	ID, Agent                     string
	Model, Effort, TranscriptPath *string
	Mode                          *mode.Mode
	PID                           *int
	Ended                         bool
}

type leadObservationTimes struct {
	Agent, Model, Effort, Mode, TranscriptPath, PID time.Time
	EndedAt                                         time.Time
}

type Entry struct {
	ID, Lead, Agent, Role, Thread, ThreadID string
	// Block is the Rex pane of a live peer session; it is empty for threads
	// that only exist in the agent. Doctor and peer commands join on it.
	Block string
	PID   int
	Input int64
	// Task is the task file a live peer was sent and has not finished. Base is
	// the working tree before that task, as path to Git status.
	Task string
	Base map[string]string
	// Mode is the lead mode the peer was started under. Asked is the last
	// question already sent to the lead, so one question makes one message.
	Mode      mode.Mode
	Asked     string
	UpdatedAt time.Time
	Closed    bool
}
type Registry struct {
	Dir string
	// LeadID binds limit ownership when the caller selected a lead explicitly.
	LeadID string
}
type state struct {
	Waiting      map[string]map[string]int       `json:"waiting,omitempty"`
	Entries      []Entry                         `json:"entries"`
	Leads        map[string]Lead                 `json:"leads"`
	Unavailable  map[string]time.Time            `json:"unavailable"`
	LimitOwners  map[string]string               `json:"limit_owners,omitempty"`
	ModeLimits   map[string]mode.Mode            `json:"mode_limits,omitempty"`
	Ended        map[string]bool                 `json:"ended,omitempty"`
	Observations map[string]leadObservationTimes `json:"observations,omitempty"`
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
	if s.Ended == nil {
		s.Ended = make(map[string]bool)
	}
	if s.Observations == nil {
		s.Observations = make(map[string]leadObservationTimes)
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
	return store.Rename(f.Name(), path)
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
	started := time.Now()
	return r.access(true, func(s *state) error {
		if lead, ok := s.Leads[id]; ok {
			if _, err := s.observeLead(LeadObservation{ID: id, Agent: lead.Agent, Ended: true}, started); err != nil {
				return err
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
	_, err := r.ObserveLead(LeadObservation{
		ID: l.ID, Agent: l.Agent, Model: &l.Model, Effort: &l.Effort,
		Mode: &l.Mode, TranscriptPath: &l.TranscriptPath, PID: &l.PID,
	}, time.Now())
	return err
}

// ObserveLead applies observations in start-time order under the registry lock,
// including initial registration and SessionEnd for a previously unknown lead.
func (r *Registry) ObserveLead(o LeadObservation, started time.Time) (Lead, error) {
	var lead Lead
	err := r.access(true, func(s *state) error {
		var err error
		lead, err = s.observeLead(o, started)
		return err
	})
	return lead, err
}

// observeLead is the single lead mutation path; access holds the registry lock.
func (s *state) observeLead(o LeadObservation, started time.Time) (Lead, error) {
	current, known := s.Leads[o.ID]
	times := s.Observations[o.ID]
	if o.Ended && known && current.Agent != o.Agent {
		return current, fmt.Errorf("lead agent changed")
	}
	if !times.EndedAt.IsZero() && started.Before(times.EndedAt) {
		return current, fmt.Errorf("lead %q has ended", o.ID)
	}
	if o.Ended {
		times.EndedAt = started
		s.Observations[o.ID] = times
		s.Ended[o.ID] = true
		delete(s.Leads, o.ID)
		delete(s.ModeLimits, o.ID)
		for agent, owner := range s.LimitOwners {
			if owner == o.ID && s.Unavailable[agent].IsZero() {
				delete(s.Unavailable, agent)
				delete(s.LimitOwners, agent)
			}
		}
		for i := range s.Entries {
			if s.Entries[i].Lead == o.ID {
				s.Entries[i].Closed = true
			}
		}
		return current, nil
	}
	// A later observation can resume an ended native session.
	if s.Ended[o.ID] && (times.EndedAt.IsZero() || !started.After(times.EndedAt)) {
		return current, fmt.Errorf("lead %q has ended", o.ID)
	}
	delete(s.Ended, o.ID)
	if !known {
		current = Lead{ID: o.ID, Target: config.Target{Agent: o.Agent}, Mode: mode.Ask}
	}
	observeField(&current.Agent, &times.Agent, &o.Agent, started)
	observeField(&current.Model, &times.Model, o.Model, started)
	observeField(&current.Effort, &times.Effort, o.Effort, started)
	observeField(&current.TranscriptPath, &times.TranscriptPath, o.TranscriptPath, started)
	observeField(&current.PID, &times.PID, o.PID, started)
	if o.Mode != nil && !started.Before(times.Mode) {
		next := *o.Mode
		if started.Equal(times.Mode) {
			next = mode.Lower(current.Mode, next)
		}
		current.Mode, times.Mode = next, started
	}
	s.Leads[o.ID], s.Observations[o.ID] = current, times
	return current, nil
}

func observeField[T any](current *T, observedAt *time.Time, value *T, started time.Time) {
	if value != nil && !started.Before(*observedAt) {
		*current, *observedAt = *value, started
	}
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

// LeadByAddress finds the registered lead whose bus address is address, then
// applies the same liveness check as Lead.
func (r *Registry) LeadByAddress(address string) (Lead, bool, error) {
	var found Lead
	var ok bool
	err := r.access(false, func(s *state) error {
		for _, l := range s.Leads {
			if Address(l) == address {
				found, ok = l, true
				break
			}
		}
		return nil
	})
	if err != nil || !ok {
		return Lead{}, false, err
	}
	return r.Lead(found.ID)
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
