package session

import (
	"crypto/rand"
	"os"
)

// Unfinished is the rule of duolog peer status: a peer's task is unfinished
// while the peer is open and the task it was sent is still recorded. A wait
// that sees the peer go idle after the task clears it.
func (e Entry) Unfinished() bool { return !e.Closed && e.Task != "" }

// watchKey keeps a peer's watch markers apart from a lead's wait markers,
// which share the registry's waiting map.
func watchKey(peer string) string { return "watch:" + peer }

// Watch records that this process waits on the peer, as duolog peer wait
// does, until release is called. A process that dies stops counting.
func (r *Registry) Watch(peer string) (release func(), err error) {
	token := rand.Text()
	if err := r.SetWaiting(watchKey(peer), token, os.Getpid()); err != nil {
		return func() {}, err
	}
	// Best effort: a marker left behind stops counting with its process.
	return func() { _ = r.SetWaiting(watchKey(peer), token, 0) }, nil
}

// Watched reports whether a live process waits on the peer, and so will tell
// the lead itself when the peer's turn ends.
func (r *Registry) Watched(peer string) (bool, error) { return r.IsWaiting(watchKey(peer)) }
