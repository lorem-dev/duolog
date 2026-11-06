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

// stopKey names one sent task: its path and its generation, which each send
// sets anew.
func (e Entry) stopKey() string { return e.Task + "\n" + e.TaskGeneration }

// ClaimStopNotice records, under the registry lock, that the lead is told
// the peer stopped with its task unfinished. It returns the entry and true
// for the first claim on that task, and false for every later one, so the
// lead hears of one task once however many turns end on it.
func (r *Registry) ClaimStopNotice(peer string) (Entry, bool, error) {
	var entry Entry
	claimed := false
	err := r.access(true, func(s *state) error {
		for i := range s.Entries {
			x := &s.Entries[i]
			if x.ID != peer || !x.Unfinished() || x.StopNotified == x.stopKey() {
				continue
			}
			x.StopNotified = x.stopKey()
			entry, claimed = *x, true
			return nil
		}
		return nil
	})
	return entry, claimed, err
}
