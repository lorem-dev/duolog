package claude

import (
	"context"
	"fmt"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/quota"
)

// Quota reads Claude's subscription quota with internal/quota, which keeps
// the read code for the account gate. Version, Timeout and IdentitySink are
// those of quota.Reader.
type Quota struct {
	Version      string
	Timeout      time.Duration
	IdentitySink func(agent, kind string, raw []byte) error
}

// Read returns the quota that the Claude process p sees. A process of another
// agent is an error, never a read.
func (q Quota) Read(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
	if p.Agent != Facts.Name {
		return account.Snapshot{}, fmt.Errorf("%s quota: cannot read a %q process", Facts.Name, p.Agent)
	}
	return quota.Reader{Version: q.Version, Timeout: q.Timeout, IdentitySink: q.IdentitySink}.ReadProcess(ctx, p)
}
