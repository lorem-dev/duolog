package paidruns

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"

	"github.com/lorem-dev/duolog/internal/quota"
)

// fingerprintDomain keeps a fingerprint from matching any other digest.
const fingerprintDomain = "duolog paid runs account fingerprint v1"

// Fingerprint names the account whose window rates were learned: the agent,
// its identity key from State.AccountKeys, the plan name and the shape of the
// read's windows (kind, group, model and length; never usage or reset times).
// A change of any of them means other windows, so learned rates and the tier
// no longer apply. The plan name only changes the fingerprint; it never
// decides an account kind or a tier. The result is a SHA-256 digest, so no
// plan name or key is kept in it.
func Fingerprint(agent, identityKey string, s quota.Snapshot) string {
	h := sha256.New()
	part := func(v string) {
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(v))))
		h.Write([]byte(v))
	}
	part(fingerprintDomain)
	part(agent)
	part(identityKey)
	part(s.Plan)
	for _, w := range windowShape(s) {
		part(w)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// windowShape lists the read's windows by scope, kind, group, model and
// length, in a stable order.
func windowShape(s quota.Snapshot) []string {
	shape := func(scope string, w quota.Window) string {
		return scope + "\x00" + w.Kind + "\x00" + w.Group + "\x00" + w.Model + "\x00" + strconv.Itoa(w.DurationMinutes)
	}
	var out []string
	for _, w := range s.Windows {
		out = append(out, shape("account", w))
	}
	for _, scope := range slices.Sorted(maps.Keys(s.ModelLimits)) {
		for _, w := range s.ModelLimits[scope] {
			out = append(out, shape("model "+scope, w))
		}
	}
	slices.Sort(out)
	return out
}
