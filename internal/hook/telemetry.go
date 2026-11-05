package hook

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/telemetry"
)

const (
	// noticeLine is said once per user, at the first session start that
	// collects.
	noticeLine = "Telemetry is local only; turn it off with duolog telemetry off."
	// noticeName marks the root once the notice was said. It is metadata:
	// its bytes count against the cap, prune never removes it and a purge of
	// everything keeps it, so the first disclosure is not repeated.
	noticeName = "notice.v1"
	// unhealthyLine is all a model learns of a store that cannot collect.
	unhealthyLine = "telemetry: unhealthy"
)

// telemetryLocation is where this platform keeps telemetry, from the
// environment the store itself never reads.
func telemetryLocation() telemetry.Location {
	home, _ := os.UserHomeDir()
	return telemetry.Location{GOOS: runtime.GOOS, Home: home, XDGStateHome: os.Getenv("XDG_STATE_HOME"), LocalAppData: os.Getenv("LOCALAPPDATA")}
}

// telemetryStart runs telemetry maintenance at a lead's session start, next
// to store.Prune, and returns the line the session sees: the notice the first
// time a user collects, the coarse unhealthy line when collection is on but
// the store cannot be used, and nothing otherwise. Pruning runs with
// collection off too, on a root that already exists; a worker does nothing.
// The line never names a path, an id, a size or a failure.
func telemetryStart(ctx context.Context, e Env) string {
	if ctx.Err() != nil || worker(e) || (e.Agent != "claude" && e.Agent != "codex") {
		return ""
	}
	if info, err := os.Stat(filepath.Join(e.Root, ".duolog")); err != nil || !info.IsDir() {
		return ""
	}
	cfg, err := config.Load(e.Root)
	if err != nil {
		return ""
	}
	t := cfg.Telemetry
	// No project: the notice and maintenance need no salt.
	s, err := telemetry.Open(telemetry.Options{Location: telemetryLocation(), Enabled: t.Enabled, RetentionDays: t.RetentionDays, PruneEveryHours: t.PruneEveryHours, MaxMegabytes: t.MaxMegabytes})
	if err != nil {
		if t.Enabled {
			return unhealthyLine
		}
		return ""
	}
	defer s.Close()
	_, pruneErr := s.Prune(false)
	if !t.Enabled {
		return ""
	}
	st, err := s.Status()
	if err != nil || pruneErr != nil || st.Protection.Refusal != "none" {
		return unhealthyLine
	}
	if claimNotice(st.Root) {
		return noticeLine
	}
	return ""
}

// claimNotice creates the notice marker with O_EXCL inside the checked root,
// so of several starts only one says the notice. It reports whether this call
// created it; a marker that cannot be created says nothing.
func claimNotice(path string) bool {
	r, err := os.OpenRoot(path)
	if err != nil {
		return false
	}
	defer r.Close()
	// The handle must be the directory Status checked, not a link put there.
	opened, err := r.Stat(".")
	if err != nil {
		return false
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) {
		return false
	}
	f, err := r.OpenFile(noticeName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false
	}
	_, _ = f.WriteString(noticeName + "\n")
	_ = f.Close()
	// duolog sets no umask; the mode is set after create.
	_ = r.Chmod(noticeName, 0600)
	return true
}
