package launch

import (
	"runtime"
	"strings"
)

// Getenv reads name from environ, NAME=value pairs, the last entry winning,
// with names compared without case on Windows; empty when it is not set.
func Getenv(environ []string, name string) string {
	value := ""
	for _, pair := range environ {
		key, v, _ := strings.Cut(pair, "=")
		if key == name || runtime.GOOS == "windows" && strings.EqualFold(key, name) {
			value = v
		}
	}
	return value
}
