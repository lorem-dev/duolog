package trust

// GateProblem says why the duolog gate cannot be shown to run for agent, or
// returns "" when it can. folder is whether Codex trusts the working folder.
func GateProblem(agent string, s State, folder bool) string {
	switch agent {
	case "claude":
		if s.HooksOff != "" {
			return s.HooksOff + " sets disableAllHooks"
		}
		if !s.Installed {
			return "Claude has no duolog gate here"
		}
	case "codex":
		if !s.Trusted {
			return "Codex hooks are not approved; open /hooks"
		}
		if !folder {
			return "Codex does not trust this folder"
		}
	}
	return s.Unproven
}
