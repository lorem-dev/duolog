package main

import (
	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/lorem-dev/duolog/internal/wiring/wiringtest"
)

// hooksJSON renders the hooks document of agent as the plugin ships it.
func hooksJSON(agent string, timeout int) ([]byte, error) {
	if agent == "claude" {
		return claude.HooksJSON(timeout)
	}
	return codex.HooksJSON(timeout)
}

// runInstall runs duolog install with the registered agents' installers and
// the test lead window, which reads no quota.
func runInstall(o install.Options) (install.Report, error) {
	d := wiring.InstallDeps()
	d.LeadWindow = wiringtest.LeadWindow
	return d.Run(o)
}
