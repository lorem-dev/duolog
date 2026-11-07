package load

// Peer is one peer of the lead, as the line counts it. Live is false for a
// peer whose pane is gone or whose process exited: it holds no session. Idle
// is a live peer that finished its turn with nothing queued.
type Peer struct {
	ID, Agent  string
	Live, Idle bool
}

// Limits are the account numbers of one agent: the session limit and live
// sessions that `duolog quota` prints, and the 5-hour window use when known.
type Limits struct {
	Limit, Live int
	Window      float64
	WindowKnown bool
}

// Build counts the lead's peers against the limits of agents, the agents the
// line covers in the order it names them. The window is the highest of the
// agents' known windows.
func Build(agents []string, peers []Peer, limits map[string]Limits) Summary {
	var s Summary
	for _, name := range agents {
		l := limits[name]
		a := Agent{Name: name, Limit: l.Limit, Live: l.Live}
		var idle []string
		for _, p := range peers {
			if p.Agent != name || !p.Live {
				continue
			}
			a.Running++
			if p.Idle {
				a.Idle++
				idle = append(idle, p.ID)
			}
		}
		if l.Limit > 0 {
			s.Agents = append(s.Agents, a)
			s.Idle = append(s.Idle, idle...)
		}
		if l.WindowKnown && (!s.WindowKnown || l.Window > s.Window) {
			s.Window, s.WindowKnown = l.Window, true
		}
	}
	return s
}
