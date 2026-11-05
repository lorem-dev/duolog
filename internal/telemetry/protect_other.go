//go:build !windows && !linux && !darwin

package telemetry

// No backup exclusion is known here; doctor reports it as unverified.
func excludeBackup(string) (bool, error) { return false, nil }

func indexExcluded(string) bool { return false }

func refuseMount(string) error { return nil }
