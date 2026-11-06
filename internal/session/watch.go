package session

import (
	"crypto/rand"
	"os"
)

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
func stopKey(e Entry) string { return e.Task + "\n" + e.TaskGeneration }

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
			if x.ID != peer || !x.Unfinished() || x.StopNotified == stopKey(*x) {
				continue
			}
			x.StopNotified = stopKey(*x)
			entry, claimed = *x, true
			return nil
		}
		return nil
	})
	return entry, claimed, err
}

// ReleaseStopNotice takes back the claim of claimed, the entry
// ClaimStopNotice returned, when its line was not sent, so the next stop on
// the same task sends it. A claim of a newer task stays.
func (r *Registry) ReleaseStopNotice(claimed Entry) error {
	return r.access(true, func(s *state) error {
		for i := range s.Entries {
			if x := &s.Entries[i]; x.ID == claimed.ID && x.StopNotified == stopKey(claimed) {
				x.StopNotified = ""
			}
		}
		return nil
	})
}
