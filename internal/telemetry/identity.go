package telemetry

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

const (
	saltName = "salt"
	saltSize = 32
	// projectContext separates project ids from any other use of the salt.
	projectContext = "project-v1\x00"
)

// errSaltLength is a salt that is not 32 bytes once its creator had time to
// write it. Collection stops; the salt is never replaced, so a project keeps
// its id until the person purges the store.
var errSaltLength = errors.New("telemetry: salt has the wrong length")

// saltWait bounds how long a process that lost the O_EXCL race waits for the
// winner to write the salt; tests shorten it.
var (
	saltWait = 2 * time.Second
	saltPoll = 10 * time.Millisecond
)

// loadSalt returns the store's salt, creating it with 32 random bytes when
// there is none. The creator writes the whole salt in one write and closes
// it before use; a process that finds a short salt waits for the creator and
// never writes, truncates or removes the file itself.
func loadSalt(r *os.Root) ([32]byte, error) {
	f, err := openRegular(r, saltName, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err == nil {
		return writeSalt(f)
	}
	if !errors.Is(err, fs.ErrExist) {
		return [saltSize]byte{}, err
	}
	deadline := time.Now().Add(saltWait)
	for {
		salt, n, err := readSalt(r)
		if err != nil || n == saltSize {
			return salt, err
		}
		// A longer salt is never one in progress.
		if n > saltSize || time.Now().After(deadline) {
			return [saltSize]byte{}, errSaltLength
		}
		race("salt short")
		time.Sleep(saltPoll)
	}
}

func writeSalt(f *os.File) ([saltSize]byte, error) {
	var salt [saltSize]byte
	race("salt")
	rand.Read(salt[:]) // never fails
	_, err := f.Write(salt[:])
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return [saltSize]byte{}, scrub(err)
	}
	return salt, nil
}

// readSalt reads at most one byte more than a salt and reports how many it
// found; the salt is only valid when n is saltSize.
func readSalt(r *os.Root) ([saltSize]byte, int, error) {
	f, err := openRegular(r, saltName, os.O_RDONLY)
	if err != nil {
		return [saltSize]byte{}, 0, err
	}
	var buf [saltSize + 1]byte
	n, err := io.ReadFull(f, buf[:])
	if cerr := f.Close(); err == nil || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		err = cerr
	}
	if err != nil {
		return [saltSize]byte{}, 0, scrub(err)
	}
	if n != saltSize {
		return [saltSize]byte{}, n, nil
	}
	return [saltSize]byte(buf[:saltSize]), n, nil
}

// ProjectID names a project's partition: the first 16 hex characters of
// HMAC-SHA256(salt, "project-v1\0" + canonicalStateRoot). The caller passes
// the StateRoot with its symlinks resolved, so all worktrees of a checkout
// share an id. The id cannot be turned back into the path, and no map from
// ids to paths is kept.
func ProjectID(salt [32]byte, canonicalStateRoot string) string {
	mac := hmac.New(sha256.New, salt[:])
	mac.Write([]byte(projectContext))
	mac.Write([]byte(canonicalStateRoot))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}
