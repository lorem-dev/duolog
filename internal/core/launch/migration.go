package launch

// MigrationLine tells the lead that an agent's workers no longer get name,
// and how to let them: the agent's paid_runs opt-in with a daily cap and the
// key in its paid env. The paid env adds to env_passthrough, so the person
// keeps the default names without writing them out.
func MigrationLine(agent, name string) string {
	key := "paid_runs." + agent
	return agent + " workers no longer get " + name + ". To let them use it, set " + key +
		".allow: true with a daily_cap_usd above 0 and list the key in " + key + ".env."
}
