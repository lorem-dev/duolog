package gate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/store"
)

// Record keeps the final permission outcome independently of its hook rendering.
type Record struct {
	Action policy.Action `json:"action"`
	policy.Decision
}

func Log(s *store.Store, a policy.Action, d policy.Decision) error {
	return logAt(s, a, d, "decisions.ndjson")
}

// LogRequest preserves the pending action with credentials withheld.
func LogRequest(s *store.Store, a policy.Action, d policy.Decision) error {
	return logAt(s, a, d, "requests.ndjson")
}

func logAt(s *store.Store, a policy.Action, d policy.Decision, name string) error {
	lock := flock.New(filepath.Join(s.Dir, "decisions.lock"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := store.Lock(ctx, lock); err != nil {
		_ = lock.Close()
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(filepath.Join(s.Dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	a.Target = safeTarget(a.Target)
	d.Reason = safeTarget(d.Reason)
	d.Rule = safeTarget(d.Rule)
	err = json.NewEncoder(f).Encode(Record{a, d})
	return errors.Join(err, f.Close())
}

// Credential-bearing text is withheld in full: shell quoting and embedded
// URLs make partial redaction unsafe. Evaluation always uses the original.
var credentialText = regexp.MustCompile(`(?i)(authorization|bearer|password|passwd|token|secret|api[_-]?key|https?://[^ /]+:[^ /]+@|(^|[[:space:]])(-u|--user|--proxy-user|--header|-h)([=[:space:]]|$)|(^|[[:space:]])-u[^[:space:]]*:|(^|[[:space:]])-h["']?[^[:space:]"']+:)`)

func safeTarget(text string) string {
	if credentialText.MatchString(text) {
		return "[redacted credential-bearing action]"
	}
	return text
}
