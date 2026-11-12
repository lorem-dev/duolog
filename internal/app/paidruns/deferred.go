package paidruns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

// DeferSettlement saves a finished worker's measurements without taking the
// account lock. Reconcile replays them before recovering abandoned holds.
// State and reservation formats stay unchanged for older binaries; until
// replay, their existing reservations continue to hold budget and slots.
func (s *Store) DeferSettlement(in paidrules.Settlement) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if !in.Started {
		return errors.New("paid runs: only a finished worker can defer settlement")
	}
	if in.Ended.IsZero() {
		in.Ended = s.now()
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return s.write(settlementFile(in.ID), data)
}

func settlementFile(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "settlement-" + hex.EncodeToString(sum[:]) + ".json"
}

// replaySettlements uses the ordinary idempotent ledger operation. Concurrent
// reconcilers can replay the same file safely; remove only after it settles.
// A file that is not a settlement goes; one that does not settle now stays
// for the next admission. Neither blocks the admission, whose reconcile then
// recovers the hold conservatively, as it did before deferred settlements.
func (s *Store) replaySettlements(ctx context.Context) error {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil //nolint:nilerr // no state folder yet: nothing deferred
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "settlement-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		path := filepath.Join(s.Dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var in paidrules.Settlement
		if json.Unmarshal(data, &in) != nil || !in.Started || entry.Name() != settlementFile(in.ID) {
			_ = os.Remove(path)
			continue
		}
		if s.Settle(ctx, in) != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("paid runs: remove deferred settlement: %w", err)
		}
	}
	return nil
}
