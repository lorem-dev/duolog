package main

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/install"
)

// install and the session start compare the version marker of the bundled
// skills with the binary's version: they are one version.
func TestBundledSkillsCarryTheBinaryVersion(t *testing.T) {
	if got := install.Version(); got != version {
		t.Fatalf("bundled skills say %q, the binary %q; run the bump-version skill", got, version)
	}
}
