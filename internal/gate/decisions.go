package gate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/store"
)

// Record keeps the policy outcome (including Ask) independently of its hook
// rendering, which currently refuses an action that needs a question.
type Record struct {
	Action Action `json:"action"`
	Decision
}

func Log(s *store.Store, a Action, d Decision) error {
	lock := flock.New(filepath.Join(s.Dir, "decisions.lock"))
	if err := lock.Lock(); err != nil {
		_ = lock.Close()
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(filepath.Join(s.Dir, "decisions.ndjson"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(Record{a, d})
	return errors.Join(err, f.Close())
}
