package paidruns

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

const (
	saltFile = "salt"
	saltSize = 32
	// The domains keep a digest of one purpose from matching another, and a
	// missing identity from matching any identity.
	identityDomain = "duolog paid runs account identity v1"
	unknownDomain  = "duolog paid runs unknown account v1"
	// identityWait bounds the lock wait of Identity, which has no context.
	identityWait = 10 * time.Second
)

// Identity returns the account key of one identity field of agent's free
// reply, kind such as quota.IdentityAccount: an HMAC-SHA256 of the field
// under the installation's salt, never the field. raw is used for the digest
// and dropped. An empty raw, a reply without the field, is the stable
// unknown bucket of the installation, agent and kind, not a random identity.
//
// The key is kept in the state under "agent/kind". A new key means another
// account: the agent's learned rates and tier go, because they described the
// old one, but its spend and reservations stay, so a new identity never
// brings a fresh budget or frees a running task. An error, such as state
// that cannot be read, records nothing; the caller's read fails with it.
//
// It is the body of quota.Reader.IdentitySink.
func (s *Store) Identity(agent, kind string, raw []byte) (string, error) {
	if agent == "" || kind == "" {
		return "", errors.New("paid runs: an identity needs an agent and a kind")
	}
	ctx, cancel := context.WithTimeout(context.Background(), identityWait)
	defer cancel()
	key := ""
	err := s.Update(ctx, func(st *State) error {
		salt, err := s.salt()
		if err != nil {
			return err
		}
		key = identityKey(salt, agent, kind, raw)
		st.recordIdentity(agent, kind, key)
		return nil
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// salt returns the installation's salt and makes it on first use. Callers
// hold the state lock, so two processes never make two. A salt that cannot
// be read is an error, never replaced: a new salt would change every key.
func (s *Store) salt() ([]byte, error) {
	path := filepath.Join(s.Dir, saltFile)
	data, err := os.ReadFile(path)
	if err == nil {
		salt, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(salt) != saltSize {
			return nil, fmt.Errorf("paid runs: %s is not a valid account salt", path)
		}
		return salt, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("paid runs: read account salt: %w", err)
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("paid runs: make account salt: %w", err)
	}
	if err := s.write(saltFile, []byte(hex.EncodeToString(salt)+"\n")); err != nil {
		return nil, err
	}
	return salt, nil
}

// identityKey is the HMAC-SHA256 under salt of the domain, agent, kind and
// raw, each prefixed with its length so no two inputs run together.
func identityKey(salt []byte, agent, kind string, raw []byte) string {
	domain := identityDomain
	if len(raw) == 0 {
		domain, raw = unknownDomain, nil
	}
	mac := hmac.New(sha256.New, salt)
	for _, part := range [][]byte{[]byte(domain), []byte(agent), []byte(kind), raw} {
		mac.Write(binary.BigEndian.AppendUint64(nil, uint64(len(part))))
		mac.Write(part)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// recordIdentity keeps agent's key of kind. A changed key drops the agent's
// rate samples and tier, and nothing else.
func (st *State) recordIdentity(agent, kind, key string) {
	name := agent + "/" + kind
	if st.AccountKeys[name] == key {
		return
	}
	st.AccountKeys[name] = key
	st.Samples = slices.DeleteFunc(st.Samples, func(r paidrules.RateSample) bool { return r.Agent == agent })
	delete(st.Tiers, agent)
}
