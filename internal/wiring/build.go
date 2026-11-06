// Package wiring builds the dependencies of the app packages from the real
// adapters and the kept packages.
package wiring

import (
	"context"
	"crypto/rand"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/models"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// DelegateDeps fills every field of delegate.Deps with the real function.
func DelegateDeps() delegate.Deps {
	return delegate.Deps{
		RunWorker:        runner.Run,
		ResolveModel:     models.Resolve,
		ReadLeadState:    session.ReadLeadState,
		ScanCapabilities: capability.ScanEnv,
		BridgeMCP:        capability.Bridge,
		NewChecker:       auth.NewForProcess,
		PaidGate:         paidGate,
		GateBinary:       workerenv.GateBinary,
		Canonical:        kit.Canonical,
	}
}

// delegateRunner binds delegate.Run to its Deps.
func delegateRunner(d delegate.Deps) delegate.Runner {
	return func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		return delegate.Run(ctx, d, r)
	}
}

// systemClock is the real time.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PeerDeps fills every field of peer.Deps with the real function. The account
// gates and the delegation are those of d.
func PeerDeps(d delegate.Deps) peer.Deps {
	return peer.Deps{
		Clock:        systemClock{},
		Pauses:       peer.Pauses{Poll: 2 * time.Second, Settle: time.Second, Send: 700 * time.Millisecond, Key: 150 * time.Millisecond, WorkGrace: 10 * time.Second},
		ResolveModel: models.Resolve,
		Notify:       kit.Notify,
		RecordPeer: func(reg *session.Registry, e session.Entry, limit int) error {
			return reg.RecordLimited(e, limit)
		},
		OnEnvFile: func(string) {},
		Delegate:  delegateRunner(d),
		PaidGate:  func(cfg *config.Config) (*paidruns.Gate, error) { return d.PaidGate(cfg) },
		HeldGate: func(agent, root string, env []string) error {
			return delegate.HeldGate(d, agent, root, env)
		},
	}
}

// ProfileDeps fills every field of profile.Deps with the real function.
func ProfileDeps(d delegate.Deps) profile.Deps {
	return profile.Deps{
		Delegate: delegateRunner(d),
		Rand: func() bool {
			var bit [1]byte
			if _, err := rand.Read(bit[:]); err != nil {
				panic(err)
			}
			return bit[0]&1 == 1
		},
		RunCheck: kit.RunCheck,
	}
}

// DoctorDeps fills every field of doctor.Deps with the real function.
func DoctorDeps(d delegate.Deps) doctor.Deps {
	return doctor.Deps{
		PaidGate:   func(cfg *config.Config) (*paidruns.Gate, error) { return d.PaidGate(cfg) },
		GateBinary: workerenv.GateBinary,
	}
}

// paidGate opens the account admission gate of one delegation: the user's
// account state, reads of the quota each built process sees, and the settings
// layers each build reads.
func paidGate(cfg *config.Config) (*paidruns.Gate, error) {
	st, err := paidruns.OpenUserState("")
	if err != nil {
		return nil, err
	}
	reader := quota.Reader{IdentitySink: func(agent, kind string, raw []byte) error {
		_, err := st.Identity(agent, kind, raw)
		return err
	}}
	return &paidruns.Gate{Store: st, Cache: &paidruns.Cache{Read: reader.ReadProcess}, Config: cfg, Layers: paidruns.ReadLayers}, nil
}
