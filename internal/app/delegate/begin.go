package delegate

import (
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/store"
)

// beginRun tells opts.Begun the lead and the run's task folder, then does what
// beginLearning does. The task folder is relative to the state dir.
func beginRun(cfg *config.Config, s *store.Store, leadID, role, id string, opts Options) (*learnRun, error) {
	if opts.Begun != nil {
		opts.Begun(leadID, "tasks/"+id)
	}
	return beginLearning(cfg, s, leadID, role, id, opts)
}
