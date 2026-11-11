// Package session persists leads, reusable workers and provider availability.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/store"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
)

type leadObservationTimes struct {
	Agent, Model, Effort, Mode, TranscriptPath, PID, Root time.Time
	EndedAt                                               time.Time
}

type Registry struct {
	Dir string
	// LeadID binds limit ownership when the caller selected a lead explicitly.
	LeadID string
	// Limit is the lead's max_live_sessions, set by OpenLead.
	Limit int
	// within, when set, bounds every wait for the registry lock: see Within.
	within context.Context
}

// Within returns the registry with every wait for its lock bounded by ctx:
// an operation tries the lock again every few milliseconds and fails with
// ctx's error once ctx ends. Best-effort notices use it, so that a process
// that stalls with the lock cannot hold back a hook.
func (r *Registry) Within(ctx context.Context) *Registry {
	c := *r
	c.within = ctx
	return &c
}

// OpenWithin is Open with the wait for the lock bounded by ctx, as Within.
func OpenWithin(ctx context.Context, dir string) (*Registry, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	r := (&Registry{Dir: dir}).Within(ctx)
	err := r.access(false, func(*state) error { return nil })
	return r, err
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
	// Delegations holds the work project of each MCP delegation by run id; a
	// lead's end keeps them, so results stay found (delegations.go).
	Delegations map[string]DelegationLocation `json:"delegations,omitempty"`
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

// OpenLead opens the registry in the lead home dir and sets Limit to the
// lead's max_live_sessions from the config of the checkout that holds dir: a
// lead's sessions in its configured submodules count against that one cap.
// work is the config the caller loaded for the work project whose main
// checkout is workRoot; when that project holds the lead home, work is the
// lead home's config and is used as it is. A lead home config that does not
// load is an error.
func OpenLead(dir string, work *config.Config, workRoot string) (*Registry, error) {
	r, err := Open(dir)
	if err != nil {
		return nil, err
	}
	r.Limit = work.Limits.MaxLiveSessions
	if home := filepath.Dir(dir); !sameCheckout(home, workRoot) {
		lead, err := configfile.Load(home)
		if err != nil {
			return nil, fmt.Errorf("lead home config: %w", err)
		}
		r.Limit = lead.Limits.MaxLiveSessions
	}
	return r, nil
}

func (r *Registry) access(write bool, fn func(*state) error) error {
	lock := flock.New(filepath.Join(r.Dir, "sessions.lock"))
	if err := store.Lock(r.within, lock); err != nil {
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

// LookupWork finds a reusable thread belonging to this exact checkout.
// Legacy entries belong only to the checkout that owns their registry.
func (r *Registry) LookupWork(lead, agent, role, thread, workRoot string) (*Entry, bool, error) {
	var found *Entry
	err := r.access(false, func(s *state) error {
		for i := len(s.Entries) - 1; i >= 0; i-- {
			x := s.Entries[i]
			if x.Closed || x.Lead != lead || x.Agent != agent || x.Role != role || x.Thread != thread {
				continue
			}
			root := x.WorkRoot
			if root == "" {
				root = filepath.Dir(r.Dir)
			}
			if sameCheckout(root, workRoot) {
				found = &x
				break
			}
		}
		return nil
	})
	return found, found != nil, err
}

func sameCheckout(a, b string) bool {
	x, xe := os.Stat(a)
	y, ye := os.Stat(b)
	return xe == nil && ye == nil && os.SameFile(x, y)
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

// ShareCap compares fixed pane groups without running Git. A deleted work
// root shares no cap. Legacy panes without a group share only their checkout.
func ShareCap(a, b Entry) bool {
	if a.WorkRoot == "" && b.WorkRoot == "" {
		return true
	}
	group := paneGroup(a)
	return group != "" && group == paneGroup(b)
}

func paneGroup(e Entry) string {
	if info, err := os.Stat(e.WorkRoot); err != nil || !info.IsDir() {
		return ""
	}
	if e.CapGroup != "" {
		return e.CapGroup
	}
	return pathutil.Clean(pathutil.Resolved(e.WorkRoot))
}

// RecordLimited retains at most limit sessions for this lead and agent (and,
// for live panes, those in its fixed cap group). A
// nonpositive limit leaves the registry unbounded. A stored thread that is over the
// limit is closed, because it costs nothing to reopen. A pane is still running, and
// nothing here can stop it, so a pane is never closed to make room: a new pane over
// the limit is refused, which also settles two opens that overlap. A pane whose
// cap group Git cannot work out is refused too.
func (r *Registry) RecordLimited(e Entry, limit int) error {
	if e.Block != "" && e.CapGroup == "" {
		group, err := project.CapGroup(e.WorkRoot, r.Dir)
		if err != nil {
			return err
		}
		e.CapGroup = group
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	return r.access(true, func(s *state) error {
		if err := s.limitEntries(e, limit); err != nil {
			return err
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

func (s *state) limitEntries(e Entry, limit int) error {
	if limit <= 0 {
		return nil
	}
	threads, panes := s.capEntries(e)
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
	return nil
}

func (s *state) capEntries(e Entry) (threads []int, panes int) {
	for i, x := range s.Entries {
		if x.Closed || x.ID == e.ID || x.Lead != e.Lead || x.Agent != e.Agent {
			continue
		}
		if x.Block == "" {
			threads = append(threads, i)
		} else if e.Block == "" || ShareCap(x, e) {
			panes++
		}
	}
	return threads, panes
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
		Mode: &l.Mode, TranscriptPath: &l.TranscriptPath, PID: &l.PID, Root: &l.Root,
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
	s.revive(o, started)
	if !known {
		current = Lead{ID: o.ID, Target: config.Target{Agent: o.Agent}, Mode: mode.Ask}
	}
	observeLeadFields(&current, &times, o, started)
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

func observeLeadFields(current *Lead, times *leadObservationTimes, o LeadObservation, started time.Time) {
	observeField(&current.Agent, &times.Agent, &o.Agent, started)
	observeField(&current.Model, &times.Model, o.Model, started)
	observeField(&current.Effort, &times.Effort, o.Effort, started)
	observeField(&current.TranscriptPath, &times.TranscriptPath, o.TranscriptPath, started)
	observeField(&current.PID, &times.PID, o.PID, started)
	observeField(&current.Root, &times.Root, o.Root, started)
}

// LeadRegisteredAt returns when the registry first saw the lead's process, the
// start of the hook that registered it, or the zero time when it does not know.
func (r *Registry) LeadRegisteredAt(id string) (time.Time, error) {
	var at time.Time
	err := r.access(false, func(s *state) error { at = s.Observations[id].PID; return nil })
	return at, err
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
