package gate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/store"
)

// Record keeps the final permission outcome independently of its hook rendering.
type Record struct {
	Action Action `json:"action"`
	Decision
}

func Log(s *store.Store, a Action, d Decision) error { return logAt(s, a, d, "decisions.ndjson") }

// LogRequest preserves the full pending action before waiting for a decision.
func LogRequest(s *store.Store, a Action, d Decision) error { return logAt(s, a, d, "requests.ndjson") }

func logAt(s *store.Store, a Action, d Decision, name string) error {
	lock := flock.New(filepath.Join(s.Dir, "decisions.lock"))
	if err := lock.Lock(); err != nil {
		_ = lock.Close()
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(filepath.Join(s.Dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(Record{a, d})
	return errors.Join(err, f.Close())
}
