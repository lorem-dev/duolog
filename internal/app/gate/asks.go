package gate

import (
	"cmp"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// OpenAsks returns every permission ask on the bus in dir that is neither
// answered nor expired, oldest first. pendingPersonAsks is OpenAsks with P set.
func OpenAsks(dir string) ([]bus.Message, error) {
	f, err := os.Open(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pending := map[string]bus.Message{}
	decoder := json.NewDecoder(f)
	for {
		var m bus.Message
		if err := decoder.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if m.K == policy.Ask && !AskExpired(m) {
			pending[strconv.FormatInt(m.N, 10)] = m
		}
		if m.K == policy.Allow || m.K == policy.Deny {
			delete(pending, m.Ref)
		}
	}
	asks := slices.Collect(maps.Values(pending))
	slices.SortFunc(asks, func(a, b bus.Message) int { return cmp.Compare(a.N, b.N) })
	return asks, nil
}
