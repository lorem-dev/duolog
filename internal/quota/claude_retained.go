package quota

import "context"

type claudeProbeFallbackKey struct{}

// WithClaudeProbeFallback supplies a retained successful read to use before
// a model probe. The caller must check the process's route key and max age
// each time fallback runs. Normal /usage and auth reads still run first.
func WithClaudeProbeFallback(ctx context.Context, fallback func() (Snapshot, bool)) context.Context {
	return context.WithValue(ctx, claudeProbeFallbackKey{}, fallback)
}

func retainedClaudeQuota(ctx context.Context, current Snapshot) (Snapshot, bool) {
	fallback, _ := ctx.Value(claudeProbeFallbackKey{}).(func() (Snapshot, bool))
	if fallback == nil {
		return Snapshot{}, false
	}
	s, ok := fallback()
	if !ok || len(s.Windows) == 0 {
		return Snapshot{}, false
	}
	// Current billing restrictions and the retained read's restrictions both
	// count. Reusing windows must never clear evidence of paid usage.
	mergeClaudeUsage(&s.Billing, current.Billing)
	s.Recalled = true
	return s, true
}
