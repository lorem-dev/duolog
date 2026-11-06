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
	Mode mode.Mode
	// Native is the raw native permission mode name the lead reported with
	// Mode. Empty when not observed.
	Native         string
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
	// Native travels with Mode: an observation that sets Mode replaces Native
	// with *Native, or with empty when Native is nil.
	Native *string
	PID    *int
	Ended  bool
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
	// WorkRoot keys live peers by checkout, while state is shared across worktrees.
	WorkRoot   string
	RexSession string
	Run        string // the pane's DUOLOG_RUN, a duolog run id; never a vendor id
	PID        int
	Input      int64
	// Task is the task file a live peer was sent and has not finished. Base is
	// the working tree before that task, as path to Git status.
	Task string
	Base map[string]string
	// Mode is the lead mode the peer was started under. Asked is the last
	// question already sent to the lead, so one question makes one message.
	Mode             mode.Mode
	Asked            string
	QuestionSince    time.Time
	QuestionNotified bool
	UpdatedAt        time.Time
	Closed           bool
	// AccountSessionID is the account reservation of a peer's pane slot, in
	// the user's account state; AdmissionID is the reservation of its latest
	// admitted task, the open's own until a send takes it over; and
	// TaskGeneration is the reservation of the sent task not yet settled, ""
	// when none is. Clearing TaskGeneration claims the settlement, so a task
	// settles once however many commands see it end.
	AccountSessionID, AdmissionID, TaskGeneration string
}
type Registry struct {
	Dir string
	// LeadID binds limit ownership when the caller selected a lead explicitly.
	LeadID string
}
type state struct {
	PeerRexSession   string                          `json:"peer_rex_session,omitempty"`
	PeerSessionRoots map[string]string               `json:"peer_session_roots,omitempty"`
	Waiting          map[string]map[string]int       `json:"waiting,omitempty"`
	ClosedPeers      map[string]string               `json:"closed_peers,omitempty"`
	Entries          []Entry                         `json:"entries"`
	Leads            map[string]Lead                 `json:"leads"`
	Unavailable      map[string]time.Time            `json:"unavailable"`
	LimitOwners      map[string]string               `json:"limit_owners,omitempty"`
	ModeLimits       map[string]mode.Mode            `json:"mode_limits,omitempty"`
	Ended            map[string]bool                 `json:"ended,omitempty"`
	Observations     map[string]leadObservationTimes `json:"observations,omitempty"`
	// AuthDeclined holds, per lead, the logins the person closed or that
	// failed, so that duolog does not ask again until the lead ends.
	AuthDeclined map[string]map[string]time.Time `json:"auth_declined,omitempty"`
	// Notices holds, per lead, the one-time notices already given, so that
	// delegate and peer open give each one once until the lead ends.
	Notices map[string]map[string]bool `json:"notices,omitempty"`
}

// PeerRexSession is the detached Rex session shared by this project's peers.
// Peer open and close serialize its lifecycle with peer-open.lock.
func (r *Registry) PeerRexSession() (string, error) {
	var id string
	err := r.access(false, func(s *state) error { id = s.PeerRexSession; return nil })
	return id, err
}

func (r *Registry) SetPeerRexSession(id string) error {
	return r.access(true, func(s *state) error { s.PeerRexSession = id; return nil })
}

// PeerSessionRoots maps each Rex peer session this project created to the
// canonical project root it was created for, so that session names group by
// root and not by folder name.
func (r *Registry) PeerSessionRoots() (map[string]string, error) {
	roots := map[string]string{}
	err := r.access(false, func(s *state) error {
		for id, root := range s.PeerSessionRoots {
			roots[id] = root
		}
		return nil
	})
	return roots, err
}

// SetPeerSessionRoot records the root of a peer session; an empty root forgets it.
func (r *Registry) SetPeerSessionRoot(id, root string) error {
	return r.access(true, func(s *state) error {
		if root == "" {
			delete(s.PeerSessionRoots, id)
			return nil
		}
		if s.PeerSessionRoots == nil {
			s.PeerSessionRoots = map[string]string{}
		}
		s.PeerSessionRoots[id] = root
		return nil
	})
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

// Update changes one entry inside the registry lock, so that two changes to
// different fields of an entry cannot overwrite each other.
func (r *Registry) Update(id string, change func(*Entry)) (Entry, error) {
	var updated Entry
	err := r.access(true, func(s *state) error {
		for i := range s.Entries {
			if s.Entries[i].ID == id {
				change(&s.Entries[i])
				s.Entries[i].UpdatedAt = time.Now()
				updated = s.Entries[i]
				return nil
			}
		}
		return fmt.Errorf("unknown session %q", id)
	})
	return updated, err
}

// RecordLimited retains at most limit sessions for this lead and agent (and
// work root for live panes). A
// nonpositive limit leaves the registry unbounded. A stored thread that is over the
// limit is closed, because it costs nothing to reopen. A pane is still running, and
// nothing here can stop it, so a pane is never closed to make room: a new pane over
// the limit is refused, which also settles two opens that overlap.
func (r *Registry) RecordLimited(e Entry, limit int) error {
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	return r.access(true, func(s *state) error {
		if limit > 0 {
			var threads []int
			panes := 0
			for i, x := range s.Entries {
				if x.Closed || x.ID == e.ID || x.Lead != e.Lead || x.Agent != e.Agent {
					continue
				}
				if x.Block != "" {
					if e.Block != "" && x.WorkRoot != e.WorkRoot {
						continue
					}
					panes++
				} else {
					threads = append(threads, i)
				}
			}
			if e.Block != "" && panes >= limit {
				return fmt.Errorf("max_live_sessions is %d and %s has %d open panes", limit, e.Agent, panes)
			}
			sort.Slice(threads, func(i, j int) bool {
				return s.Entries[threads[i]].UpdatedAt.Before(s.Entries[threads[j]].UpdatedAt)
			})
			for len(threads) > 0 && len(threads)+panes >= limit {
				s.Entries[threads[0]].Closed = true
				threads = threads[1:]
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

// RemovePeer drops a closed pane's entry, keeping only its IDs so that a
// repeated close remains harmless without accepting unknown pane IDs.
func (r *Registry) RemovePeer(id string) error {
	return r.access(true, func(s *state) error {
		for i, x := range s.Entries {
			if x.ID == id && x.Block != "" {
				if s.ClosedPeers == nil {
					s.ClosedPeers = make(map[string]string)
				}
				s.ClosedPeers[x.ID] = x.Block
				s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
				return nil
			}
		}
		return nil
	})
}

func (r *Registry) PeerRemoved(key string) (bool, error) {
	found := false
	err := r.access(false, func(s *state) error {
		for id, block := range s.ClosedPeers {
			if key == id || key == block {
				found = true
				break
			}
		}
		return nil
	})
	return found, err
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
		delete(s.AuthDeclined, o.ID)
		delete(s.Notices, o.ID)
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
		var native string
		if o.Native != nil {
			native = *o.Native
		}
		if started.Equal(times.Mode) {
			next = mode.Lower(current.Mode, next)
			if native != current.Native {
				native = ""
			}
		}
		current.Mode, current.Native, times.Mode = next, native, started
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
// DeclineAuth records that a login for key was closed or failed while lead
// lives.
func (r *Registry) DeclineAuth(lead, key string) error {
	return r.access(true, func(s *state) error {
		if s.AuthDeclined == nil {
			s.AuthDeclined = make(map[string]map[string]time.Time)
		}
		if s.AuthDeclined[lead] == nil {
			s.AuthDeclined[lead] = make(map[string]time.Time)
		}
		s.AuthDeclined[lead][key] = time.Now().UTC()
		return nil
	})
}

// AuthDeclined reports whether a login for key was declined while lead lives.
func (r *Registry) AuthDeclined(lead, key string) (bool, error) {
	declined := false
	err := r.access(false, func(s *state) error {
		_, declined = s.AuthDeclined[lead][key]
		return nil
	})
	return declined, err
}

// ClaimNotice reports whether this call is the first for key while lead
// lives. The check and the record share one registry lock, so of two commands
// that race, one gives the notice.
func (r *Registry) ClaimNotice(lead, key string) (bool, error) {
	claimed := false
	err := r.access(true, func(s *state) error {
		if s.Notices[lead][key] {
			return nil
		}
		if s.Notices == nil {
			s.Notices = make(map[string]map[string]bool)
		}
		if s.Notices[lead] == nil {
			s.Notices[lead] = make(map[string]bool)
		}
		s.Notices[lead][key], claimed = true, true
		return nil
	})
	return claimed, err
}

func (r *Registry) Entries() ([]Entry, error) {
	var entries []Entry
	err := r.access(false, func(s *state) error {
		entries = append(entries, s.Entries...)
		return nil
	})
	return entries, err
}
