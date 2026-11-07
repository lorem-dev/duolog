// Package quotatest gives tests of quota and of its callers a fake
// quota.Reader: no agent binary starts, and the fake records the processes
// it read. It also holds ReaderContract, the cases every quota.Reader meets.
// Only tests import it.
package quotatest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

// Reader has the method of quota.Reader. This package does not import quota,
// so the tests of an adapter, which may not import it, can use the contract.
type Reader interface {
	Read(ctx context.Context, p corelaunch.Process) (account.Snapshot, error)
}

// ErrFailing is what the fake returns for a process of Failing.
var ErrFailing = errors.New("quota read failed")

// Fake is a quota.Reader of one agent with a scripted snapshot. A process
// without a command fails, as one whose CLI is not installed does.
type Fake struct {
	// Agent is the agent it reads; Snapshot is what a read finds, read at
	// the time of the call; Failing makes every read fail.
	Agent    string
	Snapshot account.Snapshot
	Failing  bool

	mu sync.Mutex
	// Reads are the processes it was asked to read, in order.
	Reads []corelaunch.Process
}

// Read records p and returns Snapshot read now, or an error for a failing
// fake or a process of another agent.
func (f *Fake) Read(_ context.Context, p corelaunch.Process) (account.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, p)
	if p.Agent != f.Agent {
		return account.Snapshot{}, fmt.Errorf("quota reader of %s: not a %s process", f.Agent, p.Agent)
	}
	if f.Failing || len(p.Prefix) == 0 {
		return account.Snapshot{}, ErrFailing
	}
	s := f.Snapshot
	s.ReadAt = time.Now().UTC()
	return s, nil
}

// Case is one reader under the contract: Read is a process of its agent
// whose read finds Want, read time aside; Failing a process of its agent
// whose read fails; Other a process of another agent.
type Case struct {
	Reader               Reader
	Read, Failing, Other corelaunch.Process
	Want                 account.Snapshot
}

// ReaderContract runs the cases every quota.Reader meets: a read finds Want
// with a read time; a failing read is an error with a zero read time; a
// process of another agent is an error, never a read.
func ReaderContract(t *testing.T, newCase func(t *testing.T) Case) {
	t.Helper()
	ctx := context.Background()
	t.Run("reads", func(t *testing.T) {
		c := newCase(t)
		got, err := c.Reader.Read(ctx, c.Read)
		if err != nil {
			t.Fatal(err)
		}
		if got.ReadAt.IsZero() {
			t.Fatal("a read without a read time")
		}
		got.ReadAt = c.Want.ReadAt
		if !reflect.DeepEqual(got, c.Want) {
			t.Fatalf("Read = %+v, want %+v", got, c.Want)
		}
	})
	t.Run("failing read", func(t *testing.T) {
		c := newCase(t)
		got, err := c.Reader.Read(ctx, c.Failing)
		if err == nil || !got.ReadAt.IsZero() {
			t.Fatalf("failing Read = %+v, %v, want an error and no read time", got, err)
		}
	})
	t.Run("other agent", func(t *testing.T) {
		c := newCase(t)
		got, err := c.Reader.Read(ctx, c.Other)
		if err == nil || !got.ReadAt.IsZero() {
			t.Fatalf("Read of another agent's process = %+v, %v, want an error", got, err)
		}
	})
}
