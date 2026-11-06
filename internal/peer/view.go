package peer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/rex"
)

// Viewed is the outcome of moving one Rex client to a peer's tab.
type Viewed struct{ ID, Tab, Client, Back string }

// Line is the one line `peer view` prints. It never holds screen text.
func (v Viewed) Line() string {
	line := v.ID + " shown on client " + v.Client + ": " + v.Tab
	if v.Back != "" {
		line += "; back: " + v.Back
	}
	return line
}

// View asks Rex to show a peer's existing tab on one client. It is passive:
// it reads the record, probes the pane, lists clients and moves focus. It
// presses no key, opens nothing and writes no record. Focus is interactive,
// so the person can type into the peer; duolog cannot filter that.
func View(ctx context.Context, id, client string) (Viewed, error) {
	e, err := setupViewer(ctx)
	if err != nil {
		return Viewed{}, err
	}
	entry, err := e.find(id)
	if err != nil {
		return Viewed{}, err
	}
	if entry.Closed {
		return Viewed{}, fmt.Errorf("peer %s is closed", entry.ID)
	}
	rc := e.clientFor(entry)
	presence, err := rc.Probe(ctx, entry.Block)
	if err != nil {
		return Viewed{}, err
	}
	if presence == rex.Gone {
		return Viewed{}, fmt.Errorf("peer %s: pane is gone", entry.ID)
	}
	if client == "" {
		clients, err := e.rex.Clients(ctx)
		if err != nil {
			return Viewed{}, err
		}
		if client, err = pickClient(clients, "", os.Getenv("REX_SESSION")); err != nil {
			return Viewed{}, err
		}
	}
	if err := rc.Focus(ctx, client, entry.Block); err != nil {
		return Viewed{}, err
	}
	viewed := Viewed{ID: entry.ID, Tab: tabLabel(entry.Agent, entry.Role), Client: client}
	// Only a Rex pane has a block to come back to.
	if block := os.Getenv("REX_BLOCK"); block != "" {
		viewed.Back = backCommand(rc.Bin, client, block, os.Getenv("REX_SESSION"))
	}
	return viewed, nil
}

// backCommand is the shell command that returns the client to block. It
// names the rex that duolog found, which may be inside the Rex app and not
// in PATH.
func backCommand(bin, client, block, session string) string {
	return strings.Join([]string{shellWord(bin), "-C", shellWord(client), "focus", shellWord(block), "-s", shellWord(session)}, " ")
}

// shellWord quotes s for a POSIX shell when it holds more than plain path
// and id characters.
func shellWord(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pickClient chooses the client to move. An explicit id wins. Otherwise it is
// the one app client attached to the lead's session. Command line clients
// never count, and zero or several matches refuse.
func pickClient(clients []rex.ClientInfo, explicit, leadSession string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	var found []string
	for _, c := range clients {
		if c.Kind != "cli" && c.ID != "" && slices.Contains(c.Sessions, leadSession) {
			found = append(found, c.ID)
		}
	}
	if len(found) != 1 {
		return "", errors.New("no single Rex client shows this session; run rex client ls and pass --client <id>")
	}
	return found[0], nil
}
