package install

// hookChangeReporter is an optional description of the changes to an agent's
// hooks. It uses the same old and merged bytes as the approval comparison.
type hookChangeReporter interface {
	HookChanges(old, updated []byte, path string) []string
}

func hookChangeNotes(a Agent, p agentPlan) []string {
	reporter, ok := a.Installer.(hookChangeReporter)
	if !ok || p.plugin && !p.sharedHooks || p.hook == nil {
		return p.notes
	}
	return append(p.notes, reporter.HookChanges(p.old, p.hook.data, p.hook.path)...)
}
