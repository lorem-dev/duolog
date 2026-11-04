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
	var found *Snapshot
	scanner := scanLines(stdout)
	for scanner.Scan() {
		var event struct {
			Report *struct {
				Limits *struct {
					Windows []struct {
						Kind  string `json:"kind"`
						Group string `json:"group"`
						Scope *struct {
							Model *struct {
								DisplayName string `json:"display_name"`
							} `json:"model"`
						} `json:"scope"`
						Percent  *float64  `json:"percent"`
						ResetsAt time.Time `json:"resets_at"`
						Severity string    `json:"severity"`
						Active   bool      `json:"is_active"`
					} `json:"limits"`
					Extra *struct {
						Enabled bool     `json:"is_enabled"`
						Used    float64  `json:"used_credits"`
						Limit   *float64 `json:"monthly_limit"`
					} `json:"extra_usage"`
				} `json:"rate_limits"`
			} `json:"usage_report"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Report == nil || event.Report.Limits == nil {
			continue
		}
		limits := event.Report.Limits
		s := Snapshot{}
		valid := true
		for _, w := range limits.Windows {
			if w.Percent == nil || *w.Percent < 0 || *w.Percent > 100 {
				valid = false
				break
			}
			kind := w.Kind
			switch kind {
			case "session", "weekly_all", "weekly_opus", "weekly_sonnet", "weekly_scoped":
			default:
				kind = "other"
			}
			group := ""
			if w.Group == "weekly" || w.Group == "session" {
				group = w.Group
			}
			model := ""
			if w.Scope != nil && w.Scope.Model != nil {
				model = strings.ToLower(strings.TrimSpace(w.Scope.Model.DisplayName))
			}
			duration := 0
			if kind == "session" || (kind == "other" && group == "session") {
				duration = 300
			} else if kind == "weekly_all" || kind == "weekly_opus" || kind == "weekly_sonnet" || kind == "weekly_scoped" || (kind == "other" && group == "weekly") {
				duration = 10080
			}
			s.Windows = append(s.Windows, Window{Kind: kind, Group: group, Model: model, DurationMinutes: duration, UsedPercent: *w.Percent, ResetsAt: w.ResetsAt, Severity: safeSeverity(w.Severity), Active: w.Active})
			s.Rejected = s.Rejected || rejected(w.Severity)
		}
		if limits.Extra != nil {
			s.ExtraUsage = &ExtraUsage{Enabled: limits.Extra.Enabled, UsedCredits: limits.Extra.Used, MonthlyLimit: limits.Extra.Limit}
		}
		if valid && len(s.Windows) > 0 {
			found = &s
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
	if found == nil {
		return Snapshot{}, errors.New("no quota data; check login")
	}
	// Auth status is optional metadata. Bound it separately so it stays cheap.
	authCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	auth := command(authCtx, bin, dir, "auth", "status", "--json")
	pipe, err := auth.StdoutPipe()
	if err != nil {
		return *found, nil
	}
	if auth.Start() != nil {
		return *found, nil
	}
	closeAuthOnCancel := context.AfterFunc(authCtx, func() { _ = pipe.Close() })
	defer closeAuthOnCancel()
	data, readErr := io.ReadAll(io.LimitReader(pipe, 64*1024))
	_ = pipe.Close()
	err = auth.Wait()
	var status struct {
		LoggedIn         *bool  `json:"loggedIn"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if readErr == nil && json.Unmarshal(data, &status) == nil {
		if status.LoggedIn != nil && !*status.LoggedIn {
			return Snapshot{}, errors.New("not logged in")
		}
		if err == nil {
			found.Plan = plan(status.SubscriptionType)
		}
	}
	return *found, nil
}
