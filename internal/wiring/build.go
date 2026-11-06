// Package wiring builds the dependencies of the app packages from the real
// adapters and the kept packages.
package wiring

import (
	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/models"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
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
