package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

func readClaude(ctx context.Context, bin, dir string) (Snapshot, error) {
	cmd := command(ctx, bin, dir, "-p", "/usage", "--output-format", "stream-json", "--verbose", "--no-session-persistence")
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Snapshot{}, errors.New("cannot open usage output")
	}
	if err = cmd.Start(); err != nil {
		return Snapshot{}, errors.New("cannot start usage read")
	}
	closeOnCancel := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer closeOnCancel()
	// Keep the last report with windows; a windowless report still carries
	// billing evidence, such as rate_limits_available false.
	var found, evidence *Snapshot
	scanner := scanLines(stdout)
	for scanner.Scan() {
		s, err := parseClaudeUsage(scanner.Bytes())
		if err != nil {
			continue
		}
		if len(s.Windows) > 0 {
			found = &s
		} else {
			evidence = &s
		}
	}
	readErr := scanner.Err()
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil {
		return Snapshot{}, errors.New("usage read failed; check login")
	}
	s := Snapshot{}
	if found != nil {
		s = *found
	} else if evidence != nil {
		s = *evidence
	}
	if loggedIn := readClaudeAuth(ctx, bin, dir, &s); !loggedIn {
		return Snapshot{Billing: s.Billing}, errors.New("not logged in")
	}
	if found == nil {
		return Snapshot{Billing: s.Billing}, errors.New("no quota data; check login")
	}
	return s, nil
}

// readClaudeAuth adds the plan and route evidence of auth status to s. The
// read is bounded separately so it stays cheap; a failed read is missing
// route evidence. It reports false only for an explicit logged-out reply.
func readClaudeAuth(ctx context.Context, bin, dir string, s *Snapshot) bool {
	authCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	auth := command(authCtx, bin, dir, "auth", "status", "--json")
	pipe, err := auth.StdoutPipe()
	if err != nil {
		s.Billing.claudeRouteUnread()
		return true
	}
	if auth.Start() != nil {
		s.Billing.claudeRouteUnread()
		return true
	}
	closeAuthOnCancel := context.AfterFunc(authCtx, func() { _ = pipe.Close() })
	defer closeAuthOnCancel()
	data, readErr := io.ReadAll(io.LimitReader(pipe, 64*1024))
	_ = pipe.Close()
	err = auth.Wait()
	var status struct {
		LoggedIn         *bool   `json:"loggedIn"`
		SubscriptionType string  `json:"subscriptionType"`
		AuthMethod       *string `json:"authMethod"`
		APIProvider      *string `json:"apiProvider"`
	}
	parsed := readErr == nil && json.Unmarshal(data, &status) == nil
	if parsed && status.LoggedIn != nil && !*status.LoggedIn {
		return false
	}
	if !parsed || err != nil {
		s.Billing.claudeRouteUnread()
		return true
	}
	s.Plan = plan(status.SubscriptionType)
	s.Billing.claudeRoute(status.AuthMethod, status.APIProvider)
	return true
}

type claudeUsage struct {
	Report *struct {
		Available *bool             `json:"rate_limits_available"`
		Limits    *claudeRateLimits `json:"rate_limits"`
	} `json:"usage_report"`
}

// claudeRateLimits accepts both recorded layouts: the CLI's limits[] with a
// percent, and the SDK's named windows with a utilization of 0 to 100.
type claudeRateLimits struct {
	Windows []struct {
		Kind  string `json:"kind"`
		Group string `json:"group"`
		Scope *struct {
			Model *claudeModel `json:"model"`
		} `json:"scope"`
		Percent  *float64  `json:"percent"`
		ResetsAt time.Time `json:"resets_at"`
		Severity string    `json:"severity"`
		Active   bool      `json:"is_active"`
	} `json:"limits"`
	FiveHour          *claudeNamedWindow `json:"five_hour"`
	SevenDay          *claudeNamedWindow `json:"seven_day"`
	SevenDayOAuthApps *claudeNamedWindow `json:"seven_day_oauth_apps"`
	SevenDayOpus      *claudeNamedWindow `json:"seven_day_opus"`
	SevenDaySonnet    *claudeNamedWindow `json:"seven_day_sonnet"`
	ModelScoped       []struct {
		Model    *claudeModel       `json:"model"`
		FiveHour *claudeNamedWindow `json:"five_hour"`
		SevenDay *claudeNamedWindow `json:"seven_day"`
	} `json:"model_scoped"`
	Extra *struct {
		Enabled        bool     `json:"is_enabled"`
		Used           float64  `json:"used_credits"`
		Limit          *float64 `json:"monthly_limit"`
		DisabledReason string   `json:"overage_disabled_reason"`
	} `json:"extra_usage"`
}

type claudeModel struct {
	DisplayName string `json:"display_name"`
}

type claudeNamedWindow struct {
	Utilization *float64  `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

// parseClaudeUsage reads one /usage report. A report without windows is not
// an error: it can carry billing evidence. An invalid window rejects the
// whole report, and a shape it does not know stays unknown.
func parseClaudeUsage(data []byte) (Snapshot, error) {
	var usage claudeUsage
	if json.Unmarshal(data, &usage) != nil || usage.Report == nil {
		return Snapshot{}, errors.New("no usage report")
	}
	s := Snapshot{}
	s.Billing.WindowsAvailable = usage.Report.Available
	limits := usage.Report.Limits
	if limits == nil {
		return s, nil
	}
	add := func(kind, group string, model *claudeModel, percent *float64, reset time.Time, severity string, active bool) error {
		w, err := claudeWindow(kind, group, model, percent, reset, severity, active)
		if err != nil {
			return err
		}
		s.Windows = append(s.Windows, w)
		s.Rejected = s.Rejected || rejected(severity)
		return nil
	}
	if limits.Windows != nil {
		for _, w := range limits.Windows {
			var model *claudeModel
			if w.Scope != nil {
				model = w.Scope.Model
			}
			if err := add(w.Kind, w.Group, model, w.Percent, w.ResetsAt, w.Severity, w.Active); err != nil {
				return Snapshot{}, err
			}
		}
	} else {
		// The named windows map onto the limits[] kinds, so both layouts
		// normalize to the same windows.
		type named struct {
			w           *claudeNamedWindow
			kind, group string
			model       *claudeModel
		}
		windows := []named{
			{limits.FiveHour, "session", "session", nil},
			{limits.SevenDay, "weekly_all", "weekly", nil},
			{limits.SevenDayOAuthApps, "other", "weekly", nil},
			{limits.SevenDayOpus, "weekly_opus", "weekly", nil},
			{limits.SevenDaySonnet, "weekly_sonnet", "weekly", nil},
		}
		for _, scoped := range limits.ModelScoped {
			windows = append(windows, named{scoped.FiveHour, "other", "session", scoped.Model}, named{scoped.SevenDay, "weekly_scoped", "weekly", scoped.Model})
		}
		for _, n := range windows {
			if n.w == nil {
				continue
			}
			if err := add(n.kind, n.group, n.model, n.w.Utilization, n.w.ResetsAt, "", false); err != nil {
				return Snapshot{}, err
			}
		}
	}
	if limits.Extra != nil {
		s.ExtraUsage = &ExtraUsage{Enabled: limits.Extra.Enabled, UsedCredits: limits.Extra.Used, MonthlyLimit: limits.Extra.Limit}
		s.Billing.OverageEnabled = limits.Extra.Enabled
		s.Billing.addClaudeWorkspaceReason(limits.Extra.DisabledReason)
	}
	return s, nil
}

func claudeWindow(kind, group string, model *claudeModel, percent *float64, reset time.Time, severity string, active bool) (Window, error) {
	if percent == nil || *percent < 0 || *percent > 100 {
		return Window{}, errors.New("invalid quota window")
	}
	switch kind {
	case "session", "weekly_all", "weekly_opus", "weekly_sonnet", "weekly_scoped":
	default:
		kind = "other"
	}
	if group != "weekly" && group != "session" {
		group = ""
	}
	name := ""
	if model != nil {
		name = strings.ToLower(strings.TrimSpace(model.DisplayName))
	}
	duration := 0
	if kind == "session" || (kind == "other" && group == "session") {
		duration = 300
	} else if kind != "other" || group == "weekly" {
		duration = 10080
	}
	return Window{Kind: kind, Group: group, Model: name, DurationMinutes: duration, UsedPercent: *percent, ResetsAt: reset, Severity: safeSeverity(severity), Active: active}, nil
}
