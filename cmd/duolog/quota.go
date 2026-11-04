package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/spf13/cobra"
)

type quotaResult struct {
	*quota.Snapshot
	Unavailable   string `json:"unavailable,omitempty"`
	Cleared       bool   `json:"cleared_unavailable,omitempty"`
	RegistryError string `json:"registry_error,omitempty"`
}

func newQuotaCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "quota", Short: "Read subscription quota without a model call", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		// Read-only unless a mark is cleared: never create .duolog for a lookup.
		var reg *session.Registry
		var regErr error
		var unavailable map[string]bool
		if info, statErr := os.Stat(filepath.Join(root, ".duolog")); statErr == nil && info.IsDir() {
			reg, regErr = session.Open(filepath.Join(root, ".duolog"))
		}
		if reg != nil && regErr == nil {
			unavailable, regErr = reg.Unavailable()
		}
		results := make(map[string]quotaResult)
		read := 0
		for _, agent := range []string{"claude", "codex"} {
			s, err := (quota.Reader{Version: version}).Read(cmd.Context(), agent)
			result := quotaResult{}
			if err != nil {
				result.Unavailable = err.Error()
			} else {
				read++
				result.Snapshot = &s
				if regErr != nil {
					result.RegistryError = "cannot read session registry"
				} else if unavailable[agent] && s.Healthy(time.Now()) {
					if reg.ClearUnavailable(agent) != nil {
						result.RegistryError = "cannot clear unavailable mark"
					} else {
						result.Cleared = true
					}
				}
			}
			results[agent] = result
		}
		if asJSON {
			err = json.NewEncoder(cmd.OutOrStdout()).Encode(results)
		} else {
			err = printQuota(cmd.OutOrStdout(), results, time.Now())
		}
		if err != nil {
			return err
		}
		if read == 0 {
			return errors.New("no subscription quota available")
		}
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print quota as one JSON object")
	return cmd
}

func printQuota(w io.Writer, results map[string]quotaResult, now time.Time) error {
	var b strings.Builder
	for _, agent := range []string{"claude", "codex"} {
		r := results[agent]
		if r.Unavailable != "" {
			fmt.Fprintf(&b, "%-7s unavailable: %s\n", agent, r.Unavailable)
			continue
		}
		fmt.Fprintf(&b, "%-7s", agent)
		if r.Plan != "" {
			fmt.Fprintf(&b, " plan %s", r.Plan)
		}
		b.WriteByte('\n')
		windows := func(ws []quota.Window, prefix string) {
			for _, window := range ws {
				label := window.Kind
				if label == "session" {
					label = "5h"
				} else if label == "weekly_all" {
					label = "weekly"
				} else if label == "weekly_scoped" {
					label = "weekly"
				} else if family, ok := strings.CutPrefix(label, "weekly_"); ok && family != "" {
					label = "weekly " + family
				} else if label == "other" && window.Group != "" {
					label = window.Group
				}
				if window.Model != "" {
					if label == "other" {
						label = window.Model
					} else {
						label += " " + window.Model
					}
				}
				fmt.Fprintf(&b, "  %-7s %g%% used, %s", prefix+label, window.UsedPercent, quotaReset(window.ResetsAt, now))
				if window.Severity != "" && window.Severity != "normal" && window.Severity != "unknown" {
					fmt.Fprintf(&b, ", %s", window.Severity)
				}
				b.WriteByte('\n')
			}
		}
		windows(r.Windows, "")
		keys := make([]string, 0, len(r.ModelLimits))
		for family := range r.ModelLimits {
			keys = append(keys, family)
		}
		sort.Strings(keys)
		for _, family := range keys {
			windows(r.ModelLimits[family], family+" ")
		}
		if r.ExtraUsage != nil {
			if !r.ExtraUsage.Enabled {
				b.WriteString("  extra usage off\n")
			} else {
				fmt.Fprintf(&b, "  extra usage on, %g credits used", r.ExtraUsage.UsedCredits)
				if r.ExtraUsage.MonthlyLimit != nil {
					fmt.Fprintf(&b, " of %g", *r.ExtraUsage.MonthlyLimit)
				}
				b.WriteByte('\n')
			}
		}
		if hasQuotaCredits(r.Credits) {
			fmt.Fprintf(&b, "  credits available: %t", r.Credits.HasCredits)
			if r.Credits.Unlimited {
				b.WriteString(", unlimited")
			}
			if r.Credits.Balance != nil {
				fmt.Fprintf(&b, ", balance %s", *r.Credits.Balance)
			}
			b.WriteByte('\n')
		}
		if r.Rejected {
			b.WriteString("  quota rejected\n")
		}
		if r.SpendControlReached {
			b.WriteString("  spend control reached\n")
		}
		if r.Cleared {
			b.WriteString("  cleared unavailable mark\n")
		}
		if r.RegistryError != "" {
			fmt.Fprintf(&b, "  %s\n", r.RegistryError)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func hasQuotaCredits(credits *quota.Credits) bool {
	if credits == nil {
		return false
	}
	if credits.HasCredits || credits.Unlimited {
		return true
	}
	if credits.Balance != nil {
		balance, err := strconv.ParseFloat(*credits.Balance, 64)
		return err == nil && balance != 0
	}
	return false
}

func quotaReset(reset, now time.Time) string {
	if reset.IsZero() {
		return "reset unknown"
	}
	if !reset.After(now) {
		return "reset passed; read again"
	}
	minutes := int(reset.Sub(now) / time.Minute)
	if minutes < 1 {
		return "resets in <1m"
	}
	days, hours, mins := minutes/(24*60), (minutes/60)%24, minutes%60
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("resets in %dd %dh", days, hours)
		}
		return fmt.Sprintf("resets in %dd", days)
	}
	if hours > 0 {
		if mins > 0 {
			return fmt.Sprintf("resets in %dh %dm", hours, mins)
		}
		return fmt.Sprintf("resets in %dh", hours)
	}
	return fmt.Sprintf("resets in %dm", mins)
}
