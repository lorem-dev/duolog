// Package doctortest gives tests of doctor and of its callers a fake
// doctor.Probe: no agent binary starts, and the fake records what it was
// called with. It also holds ProbeContract, the cases every doctor.Probe
// meets. Only tests import it.
package doctortest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// ErrMissing is what a fake whose binary is missing returns.
var ErrMissing = errors.New("executable file not found in $PATH")

// ProbeCall is one Probe call the fake received.
type ProbeCall struct {
	Process corelaunch.Process
	Dir     string
	Model   string
	// DirExisted says that Dir was a folder while the call ran.
	DirExisted bool
}

// Fake is a doctor.Probe with scripted answers. Set its fields before use.
type Fake struct {
	mu sync.Mutex
	// Version is the reply to --version, Catalog the reply to "debug models".
	Version, Catalog string
	// ModelName is what Model returns, Usage what Probe measured and
	// AnswerErr what Probe returns after it ran.
	ModelName string
	Usage     usage.Probed
	AnswerErr error
	// Missing makes every call fail as a binary that is not installed.
	Missing bool
	// Calls are the Probe calls in order, Metas the Metadata calls' arguments.
	Calls []ProbeCall
	Metas [][]string
}

// NewFake returns a fake that reports version 9.9.9, an empty catalog, the
// model "fake-model" and the usage of the contract.
func NewFake() *Fake {
	return &Fake{Version: "9.9.9 (fake)\n", Catalog: `{"models":[]}`, ModelName: "fake-model", Usage: contractUsage}
}

// Model returns ModelName.
func (f *Fake) Model(*config.Config, []route.Entry) (string, error) {
	if f.ModelName == "" {
		return "", errors.New("no visible model for the fake probe")
	}
	return f.ModelName, nil
}

// Metadata answers --version and "debug models" from the script.
func (f *Fake) Metadata(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Metas = append(f.Metas, args)
	if f.Missing {
		return nil, ErrMissing
	}
	switch strings.Join(args, " ") {
	case "--version":
		return []byte(f.Version), nil
	case "debug models":
		return []byte(f.Catalog), nil
	}
	return nil, fmt.Errorf("fake probe: no reply for %q", args)
}

// Probe records the call, reports the process as started under this test
// process's pid, and returns the scripted usage and answer.
func (f *Fake) Probe(_ context.Context, _ *config.Config, p corelaunch.Process, dir, model string, started func(pid int) error) (usage.Probed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Missing {
		return usage.Probed{}, ErrMissing
	}
	info, err := os.Stat(dir)
	f.Calls = append(f.Calls, ProbeCall{Process: p, Dir: dir, Model: model, DirExisted: err == nil && info.IsDir()})
	if err := started(os.Getpid()); err != nil {
		return usage.Probed{}, err
	}
	return f.Usage, f.AnswerErr
}
