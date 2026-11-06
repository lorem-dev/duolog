package account

import (
	"time"

	"github.com/lorem-dev/duolog/internal/core/launch"
)

type Window struct {
	Kind            string    `json:"kind"`
	Group           string    `json:"group,omitempty"`
	Model           string    `json:"model,omitempty"`
	DurationMinutes int       `json:"duration_minutes,omitempty"`
	UsedPercent     float64   `json:"used_percent"`
	ResetsAt        time.Time `json:"resets_at"`
	Severity        string    `json:"severity,omitempty"`
	Active          bool      `json:"active,omitempty"`
}

type ExtraUsage struct {
	Enabled      bool     `json:"enabled"`
	UsedCredits  float64  `json:"used_credits"`
	MonthlyLimit *float64 `json:"monthly_limit"`
}

type Credits struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance,omitempty"`
}

// Snapshot contains quota fields and billing evidence only; identity and
// credentials are discarded.
type Snapshot struct {
	Plan                string              `json:"plan,omitempty"`
	Windows             []Window            `json:"windows"`
	ModelLimits         map[string][]Window `json:"model_limits,omitempty"`
	ExtraUsage          *ExtraUsage         `json:"extra_usage,omitempty"`
	Credits             *Credits            `json:"credits,omitempty"`
	Rejected            bool                `json:"rejected"`
	SpendControlReached bool                `json:"spend_control_reached,omitempty"`
	Billing             BillingFacts        `json:"billing"`
	ReadAt              time.Time           `json:"read_at"`
}

// Healthy requires current, explicit headroom in every reported window.
func (s Snapshot) Healthy(now time.Time) bool {
	if s.Rejected || s.SpendControlReached || len(s.Windows) == 0 {
		return false
	}
	check := func(windows []Window) bool {
		for _, w := range windows {
			if w.UsedPercent < 0 || w.UsedPercent >= 100 || !w.ResetsAt.After(now) || rejected(w.Severity) {
				return false
			}
		}
		return true
	}
	if !check(s.Windows) {
		return false
	}
	for _, windows := range s.ModelLimits {
		if !check(windows) {
			return false
		}
	}
	return true
}

// Rejected reports whether the account refuses runs in this window whatever
// its used percent says.
func (w Window) Rejected() bool { return rejected(w.Severity) }

func rejected(value string) bool {
	switch value {
	case "rejected", "blocked", "exceeded":
		return true
	}
	return false
}

// BillingFacts is the billing evidence of a free read. It keeps known
// spellings, safe categories and flags only: a value outside the known
// spellings becomes "unknown", and identity, credentials and raw reasons are
// discarded. Absent evidence stays absent; nothing here is inferred.
type BillingFacts struct {
	AuthMethod  string `json:"auth_method,omitempty"`
	APIProvider string `json:"api_provider,omitempty"`
	AccountType string `json:"account_type,omitempty"`
	AuthMode    string `json:"auth_mode,omitempty"`
	// RouteKnown is true when the read named a known route, paid or not. A
	// failed or unknown reply leaves it false with a signal that says why.
	RouteKnown   bool            `json:"route_known"`
	RouteSignals []launch.Signal `json:"route_signals,omitempty"`
	// WindowsAvailable is the reply's own flag, nil when the reply has none.
	WindowsAvailable     *bool `json:"windows_available,omitempty"`
	OrdinaryUsageAllowed *bool `json:"ordinary_usage_allowed,omitempty"`
	// SpendControlPresent says the reply had a spend control key that is not
	// null, even false. It is no evidence: a personal account sends one too.
	SpendControlPresent bool     `json:"spend_control_present,omitempty"`
	WorkspaceReasons    []string `json:"workspace_reasons,omitempty"`
	OverageEnabled      bool     `json:"overage_enabled,omitempty"`
	// ProviderEnvKey is the name of the variable the default Codex provider
	// reads its key from, never its value.
	ProviderEnvKey string `json:"provider_env_key,omitempty"`
	// ResetCredits is the reset credit count the read reported. duolog only
	// reads it and never spends one.
	ResetCredits *int `json:"reset_credits,omitempty"`
}
