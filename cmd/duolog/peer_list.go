package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/lorem-dev/duolog/internal/app/peer"
)

// writePeerList prints the plain list, or the table with long, and the load
// line as its last line.
func writePeerList(ctx context.Context, d *cmdDeps, w io.Writer, peers []peer.ListedPeer, long bool) error {
	if long {
		if err := writeLongList(w, peers, time.Now()); err != nil {
			return err
		}
	} else {
		for _, p := range peers {
			if _, err := fmt.Fprintf(w, "%s %s\n", p.ID, p.State); err != nil {
				return err
			}
		}
	}
	return writeLoadLine(ctx, d, w, peers)
}

// writeLongList prints the table of `peer list --long`. It holds duolog's
// own data only, never text from a peer's screen.
func writeLongList(w io.Writer, peers []peer.ListedPeer, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tROLE\tAGENT\tSTATE\tTASK\tLAST\tTAB"); err != nil {
		return err
	}
	for _, p := range peers {
		task := p.Task
		if task == "" {
			task = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Role, p.Agent, p.Display(), task, lastText(p.Last, now), p.Tab); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// agoShort is a coarse age: "<1m", "4m", "3h" or "2d". The name differs from
// telemetry.go's ago, which adds " ago" and prints "0m" under a minute.
func agoShort(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// lastText is the LAST column: "-" when nothing is known, "<kind> ? ago" when
// the time is unknown, else "<kind> <age> ago".
func lastText(l *peer.LastEvent, now time.Time) string {
	if l == nil {
		return "-"
	}
	if l.At.IsZero() {
		return l.Kind + " ? ago"
	}
	return l.Kind + " " + agoShort(now.Sub(l.At)) + " ago"
}
