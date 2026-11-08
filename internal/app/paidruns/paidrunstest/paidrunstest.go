// Package paidrunstest gives tests that run real delegations an admission
// gate that never starts an agent binary for a quota read: every read is a
// first-party subscription with empty windows. Only tests import it.
package paidrunstest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/paidstore"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// Gate is a gate on the account state in dir, which it creates, for cfg.
// Its reads answer at once with a subscription of either agent; a test may
// replace Cache.Read to answer otherwise.
func Gate(dir string, cfg *config.Config) (*paidruns.Gate, error) {
	st, err := paidruns.OpenUserState(dir)
	if err != nil {
		return nil, err
	}
	return &paidruns.Gate{Store: st, Cache: &paidstore.Cache{Read: Subscription}, Config: cfg}, nil
}

// Factory returns a gate opener for delegate.NewGate that gives every
// delegation its own account state under root, so no test's runs share or
// wait for another's slots.
func Factory(root string) func(*config.Config) (*paidruns.Gate, error) {
	return func(cfg *config.Config) (*paidruns.Gate, error) {
		dir, err := os.MkdirTemp(root, "accounts-")
		if err != nil {
			return nil, err
		}
		return Gate(dir, cfg)
	}
}

// ByWorkingDir returns a gate opener for delegate.NewGate that keeps one
// account state per working folder under root: the commands of one test,
// which runs in its own folder, share it, as a peer's open, send and close
// must, and tests in other folders never see it.
func ByWorkingDir(root string) func(*config.Config) (*paidruns.Gate, error) {
	return func(cfg *config.Config) (*paidruns.Gate, error) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(wd))
		return Gate(filepath.Join(root, hex.EncodeToString(sum[:8])), cfg)
	}
}

// Cache is a fresh route-aware cache that reads with read, for a test that
// drops what a gate's cache holds.
func Cache(read func(context.Context, launch.Process) (account.Snapshot, error)) *paidstore.Cache {
	return &paidstore.Cache{Read: read}
}

// KeepingCache is Cache that keeps its good reads in dir, as the next duolog
// command's cache finds them.
func KeepingCache(read func(context.Context, launch.Process) (account.Snapshot, error), dir string) *paidstore.Cache {
	return &paidstore.Cache{Read: read, Dir: dir}
}

// Subscription is a read of p's agent on a first-party subscription login,
// overage off, with a five-hour and a weekly window that reset later.
func Subscription(_ context.Context, p launch.Process) (account.Snapshot, error) {
	now := time.Now()
	yes := true
	s := account.Snapshot{ReadAt: now, Windows: []account.Window{
		{Kind: "session", DurationMinutes: 300, ResetsAt: now.Add(2 * time.Hour)},
		{Kind: "weekly_all", DurationMinutes: 10080, ResetsAt: now.Add(72 * time.Hour)},
	}}
	switch p.Agent {
	case "claude":
		s.Billing = account.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: &yes}
	case "codex":
		s.Billing = account.BillingFacts{AccountType: "chatgpt", AuthMode: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: &yes}
	}
	return s, nil
}
