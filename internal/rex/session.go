package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type CreatedSession struct {
	Session string
	Block   string
}

type SessionInfo struct {
	ID    string `json:"session_id"`
	Label string `json:"label"`
}

// ListSessions lists server sessions without selecting one on a client.
func (c Client) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	out, err := c.call(ctx, "session", "list", "--json")
	if err != nil {
		return nil, err
	}
	var listing struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		return nil, fmt.Errorf("rex session list: invalid reply: %w", err)
	}
	sessions := listing.Sessions[:0]
	for _, session := range listing.Sessions {
		if session.ID != "" {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func (c Client) RenameSession(ctx context.Context, label string) error {
	if c.Session == "" || label == "" {
		return errors.New("rex session rename: missing session or label")
	}
	_, err := c.call(ctx, "session", "rename", c.Session, label)
	return err
}

// NewSession creates a detached session and runs the first peer in its first
// tab. Creating a server session does not select or show it on a client.
// The CLI contract is documented at superlogical.com/rex/docs/reference/cli.
func (c Client) NewSession(ctx context.Context, label string, argv []string) (CreatedSession, error) {
	if label == "" || len(argv) == 0 {
		return CreatedSession{}, errors.New("rex new: missing session label or command")
	}
	args := []string{"new", label}
	if c.Label != "" {
		args = append(args, "--window", c.Label)
	}
	args = append(args, "--json", "--shell=none")
	if c.Dir != "" {
		args = append(args, "-c", c.Dir)
	}
	args = append(args, "--")
	args = append(args, argv...)
	out, err := c.call(ctx, args...)
	if err != nil {
		return CreatedSession{}, err
	}
	var reply struct {
		SessionID      string   `json:"session_id"`
		BlockIDs       []string `json:"block_ids"`
		InitialWindows []struct {
			BlockIDs []string `json:"block_ids"`
		} `json:"initial_windows"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return CreatedSession{}, fmt.Errorf("rex new: invalid reply: %w", err)
	}
	if reply.SessionID == "" || reply.SessionID == c.Session {
		return CreatedSession{}, errors.New("rex new: reply has no new session_id")
	}
	block := ""
	if len(reply.BlockIDs) > 0 {
		block = reply.BlockIDs[0]
	}
	if block == "" && len(reply.InitialWindows) > 0 && len(reply.InitialWindows[0].BlockIDs) > 0 {
		block = reply.InitialWindows[0].BlockIDs[0]
	}
	if block == "" {
		created := c
		created.Session = reply.SessionID
		// Some CLI versions return only the session identity for `new`.
		// There is exactly one terminal in the newly created session.
		out, listErr := created.call(ctx, "block", "ls", "-s", reply.SessionID, "--json")
		var listing struct {
			Blocks []struct {
				BlockID string `json:"block_id"`
				ID      string `json:"id"`
			} `json:"blocks"`
		}
		if listErr == nil {
			listErr = json.Unmarshal([]byte(out), &listing)
			if listErr == nil && len(listing.Blocks) == 1 {
				// Older CLIs name the block `id`, newer ones `block_id`.
				listed := listing.Blocks[0].BlockID
				if listed == "" {
					listed = listing.Blocks[0].ID
				}
				if listed != "" {
					return CreatedSession{Session: reply.SessionID, Block: listed}, nil
				}
			}
		}
		return CreatedSession{}, errors.Join(errors.New("rex new: could not identify its terminal block"), listErr, created.KillSession(ctx))
	}
	return CreatedSession{Session: reply.SessionID, Block: block}, nil
}

func (c Client) KillSession(ctx context.Context) error {
	if c.Session == "" {
		return errors.New("rex kill: missing session")
	}
	_, err := c.call(ctx, "kill", c.Session)
	if IsNotFound(err) {
		return nil
	}
	return err
}
