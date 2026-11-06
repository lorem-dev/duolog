package peer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/capability"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

// pluginOffer is Claude Code's own question whether to install a plugin, such
// as its LSP plugin recommendation.
type pluginOffer struct{ plugin, marketplace, trigger string }

var (
	offerAskRe     = regexp.MustCompile(`(?i)\binstall\b.*\bplugins?\b.*\?$`)
	offerPluginRe  = regexp.MustCompile(`^Plugin:\s*(\S+)$`)
	offerMarketRe  = regexp.MustCompile(`^Marketplace:\s*(\S+)$`)
	offerTriggerRe = regexp.MustCompile(`^Triggered by:\s*(\S.*)$`)
	offerNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// declineLabels are the options that only decline for now, in order of
// preference. "Never for this plugin" and "Disable all ..." change the
// person's own settings and are never chosen.
var declineLabels = []string{"no, not now", "not now", "no"}

// readPluginOffer recognizes a question that asks to install a plugin and
// names it on a "Plugin:" line above. A name that is not a plain word is not
// read, so the question stays with the lead.
func readPluginOffer(screen Screen) (pluginOffer, bool) {
	if screen.State != Question || len(screen.Options) == 0 {
		return pluginOffer{}, false
	}
	var offer pluginOffer
	for _, line := range strings.Split(screen.Text, "\n") {
		line = strings.TrimSpace(line)
		if corescreen.IsRule(line) {
			offer = pluginOffer{}
		}
		if m := offerPluginRe.FindStringSubmatch(line); m != nil {
			offer.plugin = m[1]
		}
		if m := offerMarketRe.FindStringSubmatch(line); m != nil {
			offer.marketplace = m[1]
		}
		if m := offerTriggerRe.FindStringSubmatch(line); m != nil {
			offer.trigger = corescreen.Cut(oneLine(m[1]), 60)
		}
		if offerAskRe.MatchString(line) {
			if offerNameRe.MatchString(offer.plugin) && (offer.marketplace == "" || offerNameRe.MatchString(offer.marketplace)) {
				return offer, true
			}
			offer = pluginOffer{}
		}
	}
	return pluginOffer{}, false
}

// installQuestion recognizes a question that asks to install a plugin without
// its metadata, which may be missing, wrapped or above the capture. A question
// line wrapped onto the next line is read joined.
func installQuestion(screen Screen) bool {
	if screen.State != Question || len(screen.Options) == 0 {
		return false
	}
	previous := ""
	for _, line := range strings.Split(screen.Text, "\n") {
		line = strings.TrimSpace(line)
		if corescreen.IsRule(line) {
			previous = ""
			continue
		}
		if offerAskRe.MatchString(line) || previous != "" && offerAskRe.MatchString(previous+" "+line) {
			return true
		}
		previous = line
	}
	return false
}

// declinePlugin answers a plugin offer with the option that declines for now.
// A worker never installs a plugin; with on_missing.plugin lead_first, the
// lead hears once per peer and plugin how the person can install it. An offer
// without a plugin name is declined only with "No, not now", and the lead
// hears nothing, since there is nothing to install.
func (e *env) declinePlugin(ctx context.Context, entry session.Entry, screen Screen, offer pluginOffer) (bool, error) {
	labels := declineLabels
	if offer.plugin == "" {
		labels = declineLabels[:1]
	}
	target := -1
	for _, want := range labels {
		target = -1
		for i, option := range screen.Options {
			if strings.ToLower(option.Label) == want {
				target = i
			}
		}
		if target >= 0 {
			break
		}
	}
	if target < 0 {
		return false, nil
	}
	label := screen.Options[target].Label
	if err := e.selectOption(ctx, entry, screen, label); err != nil {
		return false, err
	}
	action := gate.Action{Kind: "answer", Role: entry.Role, Target: screen.Question + " => " + label}
	decision := gate.Decision{Kind: gate.Allow, Reason: "a worker never installs a plugin", Rule: "capabilities.on_missing.plugin"}
	if err := gate.Log(&store.Store{Dir: e.dir}, action, decision); err != nil {
		return false, err
	}
	if setting := e.cfg.Capabilities.OnMissing.Plugin; offer.plugin != "" && (setting == "" || setting == capability.ActionLeadFirst) {
		if err := e.tellPlugin(entry, offer); err != nil {
			return false, err
		}
	}
	_, err := e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
	return true, err
}

// tellPlugin sends the lead one note per peer and plugin. A marker file under
// the state folder, created exclusively, keeps separate waits from repeating it.
func (e *env) tellPlugin(entry session.Entry, offer pluginOffer) error {
	name := offer.plugin
	if offer.marketplace != "" {
		name += "@" + offer.marketplace
	}
	dir := filepath.Join(e.dir, "plugin-offers")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	marker := filepath.Join(dir, entry.ID+"-"+name)
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	from := "Claude offered plugin " + offer.plugin
	if offer.marketplace != "" {
		from += " from " + offer.marketplace
	}
	install := "; declined. To install it: claude plugin install " + name
	text := from + " (triggered by " + offer.trigger + ")" + install
	if offer.trigger == "" {
		text = from + install
	}
	msg := bus.Message{From: entry.Agent + "/" + entry.Role, To: e.address(entry.Lead), K: "note", Ref: entry.ID, S: text}
	if _, err = e.bus.Send(msg); errors.Is(err, bus.ErrTooLong) {
		msg.S = from + install
		_, err = e.bus.Send(msg)
	}
	if err != nil {
		_ = os.Remove(marker)
	}
	return err
}
