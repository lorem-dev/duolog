//go:build !windows && !darwin && !linux

package paidruns

// processBirth is unavailable here, so a running process stays Unknown and
// its reservation held.
func processBirth(int) (string, bool) {
	return "", false
}
