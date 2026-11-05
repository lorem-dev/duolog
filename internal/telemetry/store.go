package telemetry

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
)

// Defaults of the user-level settings, used when a value is out of range.
const (
	defaultRetentionDays   = 30
	defaultPruneEveryHours = 6
	defaultMaxMegabytes    = 16
)

// envSwitch set to off turns collection off; no value turns it on.
const envSwitch = "DUOLOG_TELEMETRY"

// Options are the resolved settings of one store. The caller fills them from
// config.Load: Enabled is the user switch, ProjectEnabled is false when the
// project turned its partition off.
type Options struct {
	Location           Location
	Names              Names
	CanonicalStateRoot string // the project's StateRoot, symlinks resolved; empty outside a project
	Enabled            bool
	ProjectEnabled     bool
	RetentionDays      int
	PruneEveryHours    int
	MaxMegabytes       int
	Now                func() time.Time // defaults to time.Now
}

// Store appends events to the telemetry root. Open one per process and
// reuse it; it is safe for concurrent use.
type Store struct {
	o    Options
	path string // the telemetry root

	mu      sync.Mutex
	root    *os.Root // nil when off with no root on disk, or after Close
	project string   // the project partition id; empty when project events are off
}

// Open resolves and checks the root. With collection on it creates the root
// and, for a project, the salt. With collection off it writes nothing: an
// existing root is opened as it is, with no mode, ACL or backup exclusion
// set, for reading and maintenance, and Append writes nothing.
func Open(o Options) (*Store, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	o.RetentionDays = within(o.RetentionDays, 1, defaultRetentionDays, defaultRetentionDays)
	o.PruneEveryHours = within(o.PruneEveryHours, 1, 1<<20, defaultPruneEveryHours)
	o.MaxMegabytes = within(o.MaxMegabytes, 1, 1<<20, defaultMaxMegabytes)
	if envOff() {
		o.Enabled = false
	}
	o.ProjectEnabled = o.ProjectEnabled && o.Enabled && o.CanonicalStateRoot != ""
	path, err := ResolveRoot(o.Location)
	if err != nil {
		return nil, err
	}
	s := &Store{o: o, path: path}
	open := openProtected
	if !o.Enabled {
		if _, err := os.Lstat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return s, nil
			}
			return nil, scrub(err)
		}
		open = openExisting
	}
	r, _, err := open(path)
	if err != nil {
		return nil, err
	}
	if o.ProjectEnabled {
		salt, err := loadSalt(r)
		if err != nil {
			r.Close()
			return nil, err
		}
		s.project = ProjectID(salt, o.CanonicalStateRoot)
	}
	s.root = r
	return s, nil
}

// Close releases the root. Append after Close fails and is counted.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return scrub(err)
}

func within(n, lo, hi, fallback int) int {
	if n < lo || n > hi {
		return fallback
	}
	return n
}

func envOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(envSwitch)), "off")
}
