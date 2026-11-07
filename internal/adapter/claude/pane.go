package claude

import (
	"regexp"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/screen"
)

// Pane opens Claude Code in a peer pane.
type Pane struct{}

// Argv is the interactive command of a peer: the admitted process's launch
// argv, then the pane's own flags, none of which grants a permission. A peer
// cannot delegate: the gate denies a spawn, and without the tools the peer
// does not spend turns trying. One value with =, as the option takes a list.
// A peer that only the gate keeps read-only starts without the built-in tools
// that are not read-only either, a second wall behind the gate. m is the
// lead's mode the pane opened under.
func (Pane) Argv(r route.Route, m mode.Mode, p corelaunch.Process) []string {
	args := p.LaunchArgv("--model", r.Model)
	if r.Effort != "" {
		args = append(args, "--effort", r.Effort)
	}
	args = append(args, "--append-system-prompt", kit.BatchReadsRule)
	disallowed := []string{"Agent", "Task"}
	if mode.GateHeld(Facts.Name, m, r.Sandbox) {
		disallowed = append(disallowed, mode.HeldDisallowedTools(true)...)
	}
	return append(args, "--disallowedTools="+strings.Join(disallowed, ","))
}

// Settings are the extra settings of a pane's process: Claude's notifications
// off.
func (Pane) Settings() map[string]any {
	return map[string]any{"preferredNotifChannel": "notifications_disabled"}
}

var (
	offerPluginRe  = regexp.MustCompile(`^Plugin:\s*(\S+)$`)
	offerMarketRe  = regexp.MustCompile(`^Marketplace:\s*(\S+)$`)
	offerTriggerRe = regexp.MustCompile(`^Triggered by:\s*(\S.*)$`)
	offerNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Offer reads Claude Code's own question whether to install a plugin, such as
// its LSP plugin recommendation. A question that names its plugin on a
// "Plugin:" line above gives the plugin; one without its metadata, which may
// be missing, wrapped or above the capture, gives an offer with no plugin. A
// name that is not a plain word is not read, so the question stays with the
// lead.
func (Pane) Offer(s screen.Screen) (screen.Offer, bool) {
	if offer, ok := readOffer(s); ok {
		offer.By = Facts.Display
		if offer.Plugin != "" {
			name := offer.Plugin
			if offer.Marketplace != "" {
				name += "@" + offer.Marketplace
			}
			offer.Install = Facts.Name + " plugin install " + name
		}
		return offer, true
	}
	if screen.InstallQuestion(s) {
		return screen.Offer{By: Facts.Display}, true
	}
	return screen.Offer{}, false
}

func readOffer(s screen.Screen) (screen.Offer, bool) {
	if s.State != screen.Question || len(s.Options) == 0 {
		return screen.Offer{}, false
	}
	var offer screen.Offer
	for _, line := range strings.Split(s.Text, "\n") {
		line = strings.TrimSpace(line)
		if screen.IsRule(line) {
			offer = screen.Offer{}
		}
		if m := offerPluginRe.FindStringSubmatch(line); m != nil {
			offer.Plugin = m[1]
		}
		if m := offerMarketRe.FindStringSubmatch(line); m != nil {
			offer.Marketplace = m[1]
		}
		if m := offerTriggerRe.FindStringSubmatch(line); m != nil {
			offer.Trigger = screen.Cut(strings.Join(strings.Fields(m[1]), " "), 60)
		}
		if screen.InstallAsk(line) {
			if offerNameRe.MatchString(offer.Plugin) && (offer.Marketplace == "" || offerNameRe.MatchString(offer.Marketplace)) {
				return offer, true
			}
			offer = screen.Offer{}
		}
	}
	return screen.Offer{}, false
}
