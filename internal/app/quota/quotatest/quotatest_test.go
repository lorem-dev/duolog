package quotatest

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/account"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

func TestReaderContract(t *testing.T) {
	ReaderContract(t, func(*testing.T) Case {
		want := account.Snapshot{Plan: "pro", Windows: []account.Window{{Kind: "session", DurationMinutes: 300, UsedPercent: 15}}}
		return Case{
			Reader:  &Fake{Agent: "claude", Snapshot: want},
			Read:    corelaunch.Process{Agent: "claude", Prefix: []string{"claude"}},
			Failing: corelaunch.Process{Agent: "claude"},
			Other:   corelaunch.Process{Agent: "codex", Prefix: []string{"codex"}},
			Want:    want,
		}
	})
}
