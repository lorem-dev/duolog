package config

import "strings"

// CleanGitEnv drops the GIT_ variables from environ, so that git answers for
// the folder it is given and not for a repository the caller's environment
// points at, as a Git hook's does. GIT_CEILING_DIRECTORIES stays: it names no
// repository, only how far up the person lets Git look from that folder.
// It is here and not in package project because project depends on this one.
func CleanGitEnv(environ []string) []string {
	clean := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") || strings.HasPrefix(kv, "GIT_CEILING_DIRECTORIES=") {
			clean = append(clean, kv)
		}
	}
	return clean
}
