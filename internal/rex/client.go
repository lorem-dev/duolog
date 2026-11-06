package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ClientInfo is one connected Rex client. A client lists every session it is
// attached to; Rex names no single focused one.
type ClientInfo struct {
	ID       string   // the value -C accepts
	Kind     string   // "app", "cli" or empty when the client sent no info
	Name     string   // the person's label, else the program name; messages only
	Sessions []string // session ids the client is attached to
}

// Clients lists the connected clients, command line clients included.
func (c Client) Clients(ctx context.Context) ([]ClientInfo, error) {
	out, err := c.call(ctx, "client", "ls", "--json")
	if err != nil {
		return nil, err
	}
	var reply struct {
		Clients []struct {
			ID   string `json:"client_id"`
			Info *struct {
				Kind  string `json:"kind"`
				Name  string `json:"name"`
				Label string `json:"label"`
			} `json:"info"`
			Sessions []string `json:"session_ids"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return nil, fmt.Errorf("rex client ls: invalid reply: %w", err)
	}
	var clients []ClientInfo
	for _, raw := range reply.Clients {
		if raw.ID == "" {
			continue
		}
		info := ClientInfo{ID: raw.ID, Sessions: raw.Sessions}
		if raw.Info != nil {
			info.Kind = raw.Info.Kind
			info.Name = raw.Info.Label
			if info.Name == "" {
				info.Name = raw.Info.Name
			}
		}
		clients = append(clients, info)
	}
	return clients, nil
}

// Focus shows a block on one named client. It sends no key and creates
// nothing. The client's session switches to the block's session if Rex
// does that on focus.
func (c Client) Focus(ctx context.Context, client, block string) error {
	if client == "" || block == "" || c.Session == "" {
		return errors.New("rex focus: missing client, block or session")
	}
	_, err := c.call(ctx, "-C", client, "focus", block, "-s", c.Session)
	return err
}
