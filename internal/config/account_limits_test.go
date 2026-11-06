package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSessionLimitsDefaultsRestrictiveLayers(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.Limits
	if l.MaxTotalSessions != 10 || l.SessionsByTier != (SessionTiers{Small: 3, Medium: 5, Large: 10}) || l.SessionWaitSeconds != 60 {
		t.Errorf("session defaults = total %d, tiers %+v, wait %d; want 10, 3/5/10, 60", l.MaxTotalSessions, l.SessionsByTier, l.SessionWaitSeconds)
	}
	if l.MaxLiveSessions != 1 {
		t.Errorf("max_live_sessions = %d, want 1 as before", l.MaxLiveSessions)
	}
	if b := cfg.Balance; b != (Balance{MaxAgeSeconds: 120, MinIntervalShare: .05, Margin: .2}) {
		t.Errorf("balance defaults = %+v, want age 120, min interval 0.05, margin 0.2", b)
	}
	if DefaultSessionTiers() != l.SessionsByTier || DefaultBalance() != cfg.Balance {
		t.Errorf("exported defaults %+v %+v differ from Load's", DefaultSessionTiers(), DefaultBalance())
	}
	if len(cfg.Notices) != 0 {
		t.Errorf("default notices = %v, want none", cfg.Notices)
	}

	// Only the person's user config may raise the total or a tier above its
	// default: the person knows the plan, and duolog cannot measure it at
	// first, so an unmeasured agent need not stay at three sessions. Raising
	// one tier does not raise the next.
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "limits: {max_total_sessions: 16, sessions_by_tier: {small: 2, medium: 8, large: 12}}\nbalance: {max_age_seconds: 300, margin: 0.3}\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "limits: {session_wait_seconds: 0}\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	l = cfg.Limits
	if l.MaxTotalSessions != 16 || l.SessionsByTier != (SessionTiers{Small: 2, Medium: 8, Large: 12}) || l.SessionWaitSeconds != 0 {
		t.Errorf("user layer = total %d, tiers %+v, wait %d; want 16, 2/8/12, 0", l.MaxTotalSessions, l.SessionsByTier, l.SessionWaitSeconds)
	}
	if cfg.Balance != (Balance{MaxAgeSeconds: 300, MinIntervalShare: .05, Margin: .3}) {
		t.Errorf("trusted balance = %+v", cfg.Balance)
	}
	if got := noticeKeys(cfg.Notices); len(got) != 0 {
		t.Errorf("user layer got notices %v", got)
	}

	// Every other layer may only lower them: a local file that Git ignores,
	// or that sits in no checkout at all, is capped at the default, even when
	// the user config raised the values.
	for _, user := range []string{"", "limits: {max_total_sessions: 16, sessions_by_tier: {large: 12}}\n"} {
		root = isolatedRoot(t)
		if user != "" {
			writeConfig(t, userConfigPath(t), user)
		}
		writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "limits: {max_total_sessions: 16, sessions_by_tier: {small: 4, large: 12}}\n")
		if cfg, err = Load(root); err != nil {
			t.Fatal(err)
		}
		l = cfg.Limits
		if l.MaxTotalSessions != 10 || l.SessionsByTier != (SessionTiers{Small: 3, Medium: 5, Large: 10}) {
			t.Errorf("local layer (user %q) raised: total %d, tiers %+v; want 10, 3/5/10", user, l.MaxTotalSessions, l.SessionsByTier)
		}
		want := []string{"limits.max_total_sessions", "limits.sessions_by_tier.small", "limits.sessions_by_tier.large"}
		if got := noticeKeys(cfg.Notices); !slices.Equal(got, want) {
			t.Errorf("local layer notices (user %q) = %v, want %v", user, got, want)
		}
		for _, n := range cfg.Notices {
			if !strings.Contains(n.Reason, "default") {
				t.Errorf("notice %+v does not name the default", n)
			}
		}
	}

	// The project may only lower the total, the tiers and the wait, and
	// tighten balance; omitted entries keep the values below it.
	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "limits: {max_total_sessions: 6, session_wait_seconds: 30}\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `limits:
  max_live_sessions: 2
  max_total_sessions: 20
  sessions_by_tier: {small: 2, large: 12}
  session_wait_seconds: 90
balance: {max_age_seconds: 600, min_interval_share: 0.02, margin: 0.1}
`)
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	l = cfg.Limits
	if l.MaxTotalSessions != 6 || l.SessionsByTier != (SessionTiers{Small: 2, Medium: 5, Large: 10}) || l.SessionWaitSeconds != 30 {
		t.Errorf("project widening applied: total %d, tiers %+v, wait %d; want 6, 2/5/10, 30", l.MaxTotalSessions, l.SessionsByTier, l.SessionWaitSeconds)
	}
	if l.MaxLiveSessions != 2 {
		t.Errorf("max_live_sessions = %d, want the project's 2 as before", l.MaxLiveSessions)
	}
	if cfg.Balance != DefaultBalance() {
		t.Errorf("project loosened balance: %+v", cfg.Balance)
	}
	want := []string{
		"limits.max_total_sessions", "limits.sessions_by_tier.large", "limits.session_wait_seconds",
		"balance.max_age_seconds", "balance.min_interval_share", "balance.margin",
	}
	if got := noticeKeys(cfg.Notices); !slices.Equal(got, want) {
		t.Errorf("project notices = %v, want %v", got, want)
	}
	for _, n := range cfg.Notices {
		if !strings.Contains(n.Reason, ".duolog/config.yaml") {
			t.Errorf("notice %+v does not name the project file", n)
		}
	}

	// Lowering in the project applies; the local layer is trusted again.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "limits: {max_total_sessions: 3, sessions_by_tier: {medium: 4}, session_wait_seconds: 10}\nbalance: {max_age_seconds: 60, min_interval_share: 0.1, margin: 0.5}\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	l = cfg.Limits
	if l.MaxTotalSessions != 3 || l.SessionsByTier != (SessionTiers{Small: 3, Medium: 4, Large: 10}) || l.SessionWaitSeconds != 10 || len(cfg.Notices) != 0 {
		t.Errorf("project lowering = total %d, tiers %+v, wait %d, notices %v", l.MaxTotalSessions, l.SessionsByTier, l.SessionWaitSeconds, cfg.Notices)
	}
	if cfg.Balance != (Balance{MaxAgeSeconds: 60, MinIntervalShare: .1, Margin: .5}) {
		t.Errorf("project tightening of balance = %+v", cfg.Balance)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "limits: {max_total_sessions: 2}\n")
	if cfg, err = Load(root); err != nil || cfg.Limits.MaxTotalSessions != 2 || len(cfg.Notices) != 0 {
		t.Errorf("local total = %v, %v; want 2 without notices", cfg, err)
	}

	// Aliases and merge keys are expanded before the comparison.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `limits:
  max_total_sessions: &many 70
  session_wait_seconds: *many
  sessions_by_tier:
    <<: [{small: 9, medium: 2}, {large: 30}]
    medium: 1
`)
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	l = cfg.Limits
	if l.MaxTotalSessions != 10 || l.SessionWaitSeconds != 60 || l.SessionsByTier != (SessionTiers{Small: 3, Medium: 1, Large: 10}) {
		t.Errorf("aliased widening applied: total %d, wait %d, tiers %+v", l.MaxTotalSessions, l.SessionWaitSeconds, l.SessionsByTier)
	}
	want = []string{"limits.max_total_sessions", "limits.sessions_by_tier.small", "limits.sessions_by_tier.large", "limits.session_wait_seconds"}
	if got := noticeKeys(cfg.Notices); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Errorf("aliased notices = %v, want %v", got, want)
	}

	// Invalid values fail with the full key name, in any layer.
	cases := map[string]string{
		"limits: {max_total_sessions: 0}":              "limits.max_total_sessions",
		"limits: {max_total_sessions: -2}":             "limits.max_total_sessions",
		"limits: {max_total_sessions: .inf}":           "limits.max_total_sessions",
		"limits: {max_total_sessions: ten}":            "limits.max_total_sessions",
		"limits: {sessions_by_tier: {small: 0}}":       "limits.sessions_by_tier.small",
		"limits: {sessions_by_tier: {large: -1}}":      "limits.sessions_by_tier.large",
		"limits: {sessions_by_tier: {huge: 4}}":        "limits.sessions_by_tier.huge",
		"limits: {sessions_by_tier: {medium: 2.5}}":    "limits.sessions_by_tier.medium",
		"limits: {session_wait_seconds: -1}":           "limits.session_wait_seconds",
		"limits: {session_wait_seconds: .nan}":         "limits.session_wait_seconds",
		"limits: {max_sessions: 3}":                    "limits.max_sessions",
		"balance: {max_age_seconds: 0}":                "balance.max_age_seconds",
		"balance: {max_age_seconds: -5}":               "balance.max_age_seconds",
		"balance: {min_interval_share: 0}":             "balance.min_interval_share",
		"balance: {min_interval_share: 1.5}":           "balance.min_interval_share",
		"balance: {min_interval_share: .nan}":          "balance.min_interval_share",
		"balance: {min_interval_share: .inf}":          "balance.min_interval_share",
		"balance: {margin: 1}":                         "balance.margin",
		"balance: {margin: -0.1}":                      "balance.margin",
		"balance: {margin: .nan}":                      "balance.margin",
		"balance: {margin: high}":                      "balance.margin",
		"balance: {window: weekly}":                    "balance.window",
		"limits: {sessions_by_tier: {small: null}}":    "limits.sessions_by_tier.small",
		"balance: {max_age_seconds: 1, margin: -.inf}": "balance.margin",
		"limits: {sessions_by_tier: [3, 5, 10]}":       "limits.sessions_by_tier",
	}
	for text, want := range cases {
		for _, file := range []string{"config.local.yaml", "config.yaml"} {
			root := isolatedRoot(t)
			writeConfig(t, filepath.Join(root, ".duolog", file), text+"\n")
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s in %s: got %v, want an error with %q", text, file, err, want)
			}
		}
	}

	// The generated schema carries the same bounds.
	s := paidSchema(t)
	for _, text := range []string{
		"limits: {max_total_sessions: 0}",
		"limits: {sessions_by_tier: {small: 0}}",
		"limits: {sessions_by_tier: {huge: 4}}",
		"limits: {sessions_by_tier: {medium: 2.5}}",
		"limits: {session_wait_seconds: -1}",
		"balance: {max_age_seconds: 0}",
		"balance: {min_interval_share: 0}",
		"balance: {min_interval_share: 1.5}",
		"balance: {margin: 1}",
		"balance: {margin: -0.1}",
		"balance: {window: weekly}",
	} {
		if err := schemaValidates(t, s, text); err == nil {
			t.Errorf("schema accepted %s", text)
		}
	}
	for _, text := range []string{
		string(defaultConfig),
		"limits: {max_total_sessions: 4, sessions_by_tier: {medium: 3, large: 3}}",
		"limits: {max_total_sessions: 20, session_wait_seconds: 0}",
		"balance: {max_age_seconds: 30, min_interval_share: 1, margin: 0}",
	} {
		if err := schemaValidates(t, s, text); err != nil {
			t.Errorf("schema rejected %s: %v", text, err)
		}
	}
}
