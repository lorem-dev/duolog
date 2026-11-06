package peer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/capability"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/store"
)

// declineLabels are the options that only decline for now, in order of
// preference. "Never for this plugin" and "Disable all ..." change the
// person's own settings and are never chosen.
var declineLabels = []string{"no, not now", "not now", "no"}

// declinePlugin answers a plugin offer with the option that declines for now.
// A worker never installs a plugin; with on_missing.plugin lead_first, the
// lead hears once per peer and plugin how the person can install it. An offer
// without a plugin name is declined only with "No, not now", and the lead
// hears nothing, since there is nothing to install.
func (e *env) declinePlugin(ctx context.Context, entry session.Entry, screen Screen, offer corescreen.Offer) (bool, error) {
	labels := declineLabels
	if offer.Plugin == "" {
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
	if setting := e.cfg.Capabilities.OnMissing.Plugin; offer.Plugin != "" && (setting == "" || setting == capability.ActionLeadFirst) {
		if err := e.tellPlugin(entry, offer); err != nil {
			return false, err
		}
	}
	_, err := e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
	return true, err
}

// tellPlugin sends the lead one note per peer and plugin. A marker file under
// the state folder, created exclusively, keeps separate waits from repeating it.
func (e *env) tellPlugin(entry session.Entry, offer corescreen.Offer) error {
	name := offer.Plugin
	if offer.Marketplace != "" {
		name += "@" + offer.Marketplace
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
	from := offer.By + " offered plugin " + offer.Plugin
	if offer.Marketplace != "" {
		from += " from " + offer.Marketplace
	}
	install := "; declined. To install it: " + offer.Install
	text := from + " (triggered by " + offer.Trigger + ")" + install
	if offer.Trigger == "" {
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
