package calibration

import (
	"slices"
	"strings"
)

func validationCommands(checks []string, hidden bool) []string {
	if !hidden {
		return checks
	}
	checks = slices.Clone(checks)
	for i, command := range checks {
		command = strings.TrimSpace(command)
		if tail, ok := strings.CutPrefix(command, "go test"); ok && (tail == "" || strings.HasPrefix(tail, " ")) {
			checks[i] = "go test -count=1" + tail
		}
	}
	return checks
}
