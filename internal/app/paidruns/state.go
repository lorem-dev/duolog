package paidruns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/store"
)

const (
	// stateVersion is the layout of state.json. Another version is refused,
	// never read as empty.
	stateVersion = 1
	stateFile    = "state.json"
	lockFile     = "accounts.lock"
)

// publish replaces a state file with a closed temporary one; tests make it
// fail as an interrupted write would.
var publish = store.Rename

// State is the account state that every project of the user shares: money,
// reservations, rates and session slots belong to the account, not to a
// project. AccountKeys maps "agent/kind" to a salted identity digest; Tiers
// is keyed by agent, and so is Readings, the latest refused admission's
// windows. It never holds a raw identity or a route value.
type State struct {
	Version      int                              `json:"version"`
	Reservations map[string]paidrules.Reservation `json:"reservations"`
	Settled      map[string]paidrules.Settlement  `json:"settled"`
	AccountKeys  map[string]string                `json:"account_keys"`
	Samples      []paidrules.RateSample           `json:"samples"`
	Tiers        map[string]paidrules.TierResult  `json:"tiers"`
	Readings     map[string]paidrules.Reading     `json:"readings,omitempty"`
	Tickets      []paidrules.Ticket               `json:"tickets"`
	NextTicket   uint64                           `json:"next_ticket"`
}

// Store is the account state of the user, in one folder that every project
// shares. Dir is that folder; Now is the clock, nil meaning time.Now.
type Store struct {
	Dir string
	Now func() time.Time
}

// OpenUserState opens the account state in dir, by default the user config
// folder's duolog/accounts, never a project folder. It makes the folder,
// private to the user, and tightens one that exists.
func OpenUserState(dir string) (*Store, error) {
	if dir == "" {
		userDir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("user config directory: %w", err)
		}
		dir = filepath.Join(userDir, "duolog", "accounts")
	}
	// A relative folder would follow the working directory into a project.
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("paid runs: account state folder %s is not absolute", dir)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("paid runs: account state folder: %w", err)
	}
	// MkdirAll keeps the mode of a folder that exists.
	if err := os.Chmod(dir, 0700); err != nil { //nolint:gosec // G302: a folder needs the x bit; 0700 keeps it the owner's alone
		return nil, fmt.Errorf("paid runs: account state folder: %w", err)
	}
	return &Store{Dir: dir}, nil
}

// Update runs fn on the state under the folder's lock and writes the result
// in its place, so that no other process sees or makes a change in between.
// A missing state starts empty. State that cannot be read, is not valid or
// has another version refuses: fn does not run and nothing is written, so
// admission stops rather than starting from a fresh budget. An error from fn
// writes nothing, and an interrupted write keeps the previous state. The
// lock waits until ctx ends; never hold it over a vendor read or a run.
func (s *Store) Update(ctx context.Context, fn func(*State) error) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	old, st, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(&st); err != nil {
		return err
	}
	st.Version = stateVersion
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("paid runs: encode account state: %w", err)
	}
	data = append(data, '\n')
	if bytes.Equal(data, old) {
		return nil
	}
	return s.write(stateFile, data)
}

// View runs fn on the state under the folder's lock and writes nothing: a
// report reads the account state without changing or creating it. Missing
// state is empty; state that cannot be read is an error.
func (s *Store) View(ctx context.Context, fn func(State) error) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, st, err := s.load()
	if err != nil {
		return err
	}
	return fn(st)
}

func (s *Store) lock(ctx context.Context) (func(), error) {
	l := flock.New(filepath.Join(s.Dir, lockFile))
	locked, err := l.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("paid runs: lock account state: %w", err)
	}
	if !locked {
		_ = l.Close()
		return nil, errors.New("paid runs: account state is locked")
	}
	return func() { _ = l.Close() }, nil
}

// load reads the state and its bytes. It never quotes the file: a refusal
// names the file only.
func (s *Store) load() ([]byte, State, error) {
	path := filepath.Join(s.Dir, stateFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		st := State{}
		st.init()
		return nil, st, nil
	}
	if err != nil {
		return nil, State{}, fmt.Errorf("paid runs: read account state: %w", err)
	}
	var st State
	if json.Unmarshal(data, &st) != nil {
		return nil, State{}, fmt.Errorf("paid runs: %s is not valid account state; paid runs and new sessions stay refused until it is repaired", path)
	}
	if st.Version != stateVersion {
		return nil, State{}, fmt.Errorf("paid runs: %s has account state version %d, not %d; paid runs and new sessions stay refused", path, st.Version, stateVersion)
	}
	st.init()
	return data, st, nil
}

// init gives every collection of st a value, so fn can add to it.
func (st *State) init() {
	st.Version = stateVersion
	if st.Reservations == nil {
		st.Reservations = map[string]paidrules.Reservation{}
	}
	if st.Settled == nil {
		st.Settled = map[string]paidrules.Settlement{}
	}
	if st.AccountKeys == nil {
		st.AccountKeys = map[string]string{}
	}
	if st.Tiers == nil {
		st.Tiers = map[string]paidrules.TierResult{}
	}
	if st.Readings == nil {
		st.Readings = map[string]paidrules.Reading{}
	}
}

// write replaces the folder's file name with data through a temporary file,
// private to the user, so a reader never sees half a file and a failed
// write keeps the previous one.
func (s *Store) write(name string, data []byte) error {
	tmp, err := os.CreateTemp(s.Dir, "."+name+"-*")
	if err != nil {
		return fmt.Errorf("paid runs: write account state: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("paid runs: write account state: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return fmt.Errorf("paid runs: write account state: %w", err)
	}
	if err := publish(tmp.Name(), filepath.Join(s.Dir, name)); err != nil {
		return fmt.Errorf("paid runs: write account state: %w", err)
	}
	return nil
}
