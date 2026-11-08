package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
)

// The read errors of a /usage that printed no report. It said nothing about
// the account, so they are transient, unlike the same words after a report.
const (
	errUsageSilent account.TransientError = "usage read failed; check login"
	errQuotaSilent account.TransientError = "no quota data; check login"
)

// errNoReport is the parse error of a line that holds no usage report.
var errNoReport = errors.New("no usage report")

func readClaude(ctx context.Context, run runner) (Snapshot, error) {
	cmd := run.command(ctx, "-p", "/usage", "--output-format", "stream-json", "--verbose", "--no-session-persistence")
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
	scanner := scanLines(stdout)
	usage := scanClaudeUsage(scanner)
	readErr := scanner.Err()
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil {
		return claudeFailedRead(ctx, run, usage)
	}
	s := usage.snapshot()
	loggedIn, err := readClaudeAuth(ctx, run, &s)
	if err != nil {
		return Snapshot{Billing: s.Billing}, err
	}
	if !loggedIn {
		return Snapshot{Billing: s.Billing}, errors.New("not logged in")
	}
	if usage.found == nil {
		return Snapshot{Billing: s.Billing}, usage.noQuota(s.Billing)
	}
	return s, nil
}

// claudeFailedRead is the result of a /usage run that did not end well. One
// that printed no report is transient only while auth status still names a
// subscription, as for a run that ended well without windows: a login that is
// out, on an API key, on another provider or unreadable is an answer, and no
// kept read may stand in for it. A read out of time leaves no time to ask.
func claudeFailedRead(ctx context.Context, run runner, usage claudeReports) (Snapshot, error) {
	failure := usage.failed()
	if !account.IsTransient(failure) || ctx.Err() != nil {
		return Snapshot{}, failure
	}
	s := usage.snapshot()
	loggedIn, err := readClaudeAuth(ctx, run, &s)
	switch {
	case err != nil:
		return Snapshot{Billing: s.Billing}, err
	case !loggedIn:
		return Snapshot{Billing: s.Billing}, errors.New("not logged in")
	case len(s.Billing.RouteSignals) > 0:
		return Snapshot{Billing: s.Billing}, errors.New(failure.Error())
	}
	return Snapshot{Billing: s.Billing}, failure
}

// claudeReports is what the lines of one /usage run held: the last report
// with windows, the last without, every report's billing evidence, such as
// rate_limits_available false, whatever its order, and whether any line held
// a usage report, even an invalid one.
type claudeReports struct {
	found, evidence *Snapshot
	billing         BillingFacts
	reported        bool
}

func scanClaudeUsage(scanner *bufio.Scanner) claudeReports {
	var r claudeReports
	for scanner.Scan() {
		s, err := parseClaudeUsage(scanner.Bytes())
		r.reported = r.reported || !errors.Is(err, errNoReport)
		if err != nil {
			continue
		}
		mergeClaudeUsage(&r.billing, s.Billing)
		if len(s.Windows) > 0 {
			r.found = &s
		} else {
			r.evidence = &s
		}
	}
	return r
}

// snapshot is the best report with the billing evidence of all of them.
func (r claudeReports) snapshot() Snapshot {
	s := Snapshot{}
	if r.found != nil {
		s = *r.found
	} else if r.evidence != nil {
		s = *r.evidence
	}
	s.Billing = r.billing
	return s
}

// failed is the error of a /usage run that did not end well.
func (r claudeReports) failed() error {
	if !r.reported {
		return errUsageSilent
	}
	return errors.New("usage read failed; check login")
}

// noQuota is the error of a /usage run without windows, on billing b.
func (r claudeReports) noQuota(b BillingFacts) error {
	if !r.reported && len(b.RouteSignals) == 0 {
		return errQuotaSilent
	}
	return errors.New("no quota data; check login")
}

// mergeClaudeUsage folds the billing evidence of one /usage report into b:
// any report that says windows are not available, that overage is on or
// that names a workspace reason counts, so a later report cannot hide an
// earlier one's evidence, nor the reverse.
func mergeClaudeUsage(b *BillingFacts, from BillingFacts) {
	if a := from.WindowsAvailable; a != nil && (b.WindowsAvailable == nil || !*a) {
		available := *a
		b.WindowsAvailable = &available
	}
	b.OverageEnabled = b.OverageEnabled || from.OverageEnabled
	for _, reason := range from.WorkspaceReasons {
		addWorkspaceReason(b, reason)
	}
}

// claudeAuthTimeout bounds auth status in a first read. A cold Node start,
// the npm shim on Windows or a busy machine can take seconds, so it leaves
// room for one inside the read's own timeout, which still wins. auth status
// starts only after /usage has ended, so the two starts share that timeout.
var claudeAuthTimeout = 10 * time.Second

// readClaudeAuth adds the plan and route evidence of auth status to s. The
// read is bounded by run.authTimeout so it stays cheap; a failed read is
// missing route evidence, and one that times out is an unknown route,
// auth:timeout. Both are paid. It reports false only for an explicit
// logged-out reply. The account and org of a read reply go to the identity
// sink, whose error it returns; a failed read hands it nothing.
func readClaudeAuth(ctx context.Context, run runner, s *Snapshot) (bool, error) {
	authCtx, cancel := context.WithTimeout(ctx, run.authTimeout)
	defer cancel()
	auth := run.command(authCtx, "auth", "status", "--json")
	pipe, err := auth.StdoutPipe()
	if err != nil {
		claudeRouteUnread(&s.Billing)
		return true, nil
	}
	if auth.Start() != nil {
		claudeRouteUnread(&s.Billing)
		return true, nil
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
		return false, nil
	}
	if !parsed || err != nil {
		if authCtx.Err() != nil {
			claudeRouteTimedOut(&s.Billing)
		} else {
			claudeRouteUnread(&s.Billing)
		}
		return true, nil
	}
	s.Plan = plan(status.SubscriptionType)
	claudeRoute(&s.Billing, status.AuthMethod, status.APIProvider)
	var identity fields
	_ = json.Unmarshal(data, &identity)
	return true, run.identify(identity.identity("email"), identity.identity("orgId"))
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
		return Snapshot{}, errNoReport
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
		addClaudeWorkspaceReason(&s.Billing, limits.Extra.DisabledReason)
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
