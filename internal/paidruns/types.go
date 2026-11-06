package paidruns

import (
	"time"

	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// WindowKey names one quota window of an agent: its kind, the model family
// it limits, empty for the whole account, and its reset time.
type WindowKey struct {
	Kind   string    `json:"kind"`
	Family string    `json:"family,omitempty"`
	Reset  time.Time `json:"reset"`
}

// WindowCost is a task's place in one window, as raw fractions of the whole
// window: the share used, the share reserved by tasks in flight, this task's
// estimated cost and the reserve kept back. The usable share divides them
// only when they are compared.
type WindowCost struct {
	Key      WindowKey `json:"key"`
	Used     float64   `json:"used"`
	Reserved float64   `json:"reserved"`
	Cost     float64   `json:"cost"`
	Reserve  float64   `json:"reserve"`
}

// Estimate is what a task is expected to use: tokens, Cached a subset of
// Input, the money they cost and its window costs. Source names how it was
// made, such as the journal's p90 or the cold bound.
type Estimate struct {
	Input   int64        `json:"input"`
	Cached  int64        `json:"cached"`
	Output  int64        `json:"output"`
	USD     float64      `json:"usd"`
	Windows []WindowCost `json:"windows,omitempty"`
	Source  string       `json:"source"`
}

// Owner is a process that holds a reservation: its PID and birth identity,
// and for a peer its pane and peer session.
type Owner struct {
	PID         int    `json:"pid,omitempty"`
	Birth       string `json:"birth,omitempty"`
	Pane        string `json:"pane,omitempty"`
	PeerSession string `json:"peer_session,omitempty"`
}

// Reservation holds an admitted task or session until it settles or is
// proven never to have started. AccountKey is a salted digest, never an
// identity. Launching marks that its supervisor began to start the child,
// so a dead supervisor no longer proves that nothing started. Retained says
// why reconciliation kept it without a safe conclusion, "" when it did not.
type Reservation struct {
	ID         string            `json:"id"`
	SessionID  string            `json:"session_id,omitempty"`
	Agent      string            `json:"agent"`
	AccountKey string            `json:"account_key,omitempty"`
	Role       string            `json:"role,omitempty"`
	Class      string            `json:"class,omitempty"`
	Family     string            `json:"family,omitempty"`
	Shape      workerenv.Shape   `json:"shape,omitempty"`
	Variant    workerenv.Variant `json:"variant,omitempty"`
	Supervisor Owner             `json:"supervisor"`
	Child      Owner             `json:"child"`
	Created    time.Time         `json:"created"`
	Estimate   Estimate          `json:"estimate"`
	Paid       bool              `json:"paid,omitempty"`
	Workspace  bool              `json:"workspace,omitempty"`
	Session    bool              `json:"session,omitempty"`
	Task       bool              `json:"task,omitempty"`
	Started    bool              `json:"started,omitempty"`
	Launching  bool              `json:"launching,omitempty"`
	Retained   string            `json:"retained,omitempty"`
	// Route is the RouteKey of the process admitted: names only. A peer
	// send must be built on its pane's route.
	Route string `json:"route,omitempty"`
	// LaunchToken is the file the launcher must take before it starts the
	// child, "" for none; see Store.LaunchWithToken.
	LaunchToken string `json:"launch_token,omitempty"`
	// Step is the receipt of the calibration step that holds it, "" for none.
	Step string `json:"step,omitempty"`
}

// Settlement is the measured end of a reservation. The Store computes
// ChargedUSD from the priced and reported costs; a caller cannot set spend.
// ReportedUSD is nil when the CLI reported no cost.
type Settlement struct {
	ID            string            `json:"id"`
	Agent         string            `json:"agent"`
	AccountKey    string            `json:"account_key,omitempty"`
	ChargedUSD    float64           `json:"charged_usd"`
	Input         int64             `json:"input"`
	Cached        int64             `json:"cached"`
	Output        int64             `json:"output"`
	ReportedUSD   *float64          `json:"reported_usd,omitempty"`
	PricedUSD     float64           `json:"priced_usd"`
	Evidence      quota.RunEvidence `json:"evidence"`
	Started       bool              `json:"started,omitempty"`
	CompleteUsage bool              `json:"complete_usage,omitempty"`
	Ended         time.Time         `json:"ended"`
}

// RateSample is a learned window rate of one agent, family and account
// fingerprint: the window's movement over WeightedTokens, in real units,
// from Intervals qualified intervals.
type RateSample struct {
	Agent          string    `json:"agent"`
	Family         string    `json:"family"`
	Fingerprint    string    `json:"fingerprint"`
	Window         WindowKey `json:"window"`
	Movement       float64   `json:"movement"`
	WeightedTokens float64   `json:"weighted_tokens"`
	Intervals      int       `json:"intervals"`
}

// TierResult is an agent's session tier, the reason for it, its rates per
// window, its session limit and its live sessions.
type TierResult struct {
	Tier   string             `json:"tier"`
	Reason string             `json:"reason,omitempty"`
	Rates  map[string]float64 `json:"rates,omitempty"`
	Limit  int                `json:"limit"`
	Live   int                `json:"live"`
}

// Ticket is a waiter for a session slot, served in Sequence order. Owner is
// the waiting process, so a dead waiter can be proven gone; Expires is when
// it stops waiting, zero when it waits until a slot or a cancellation.
type Ticket struct {
	ID       string    `json:"id"`
	Agent    string    `json:"agent"`
	Sequence uint64    `json:"sequence"`
	Created  time.Time `json:"created"`
	Owner    Owner     `json:"owner"`
	Expires  time.Time `json:"expires"`
	// Step is the receipt of the calibration step that waits, "" for none.
	Step string `json:"step,omitempty"`
}

// State is the account state that every project of the user shares: money,
// reservations, rates and session slots belong to the account, not to a
// project. AccountKeys maps "agent/kind" to a salted identity digest; Tiers
// is keyed by agent. It never holds a raw identity or a route value.
type State struct {
	Version      int                    `json:"version"`
	Reservations map[string]Reservation `json:"reservations"`
	Settled      map[string]Settlement  `json:"settled"`
	AccountKeys  map[string]string      `json:"account_keys"`
	Samples      []RateSample           `json:"samples"`
	Tiers        map[string]TierResult  `json:"tiers"`
	Tickets      []Ticket               `json:"tickets"`
	NextTicket   uint64                 `json:"next_ticket"`
}
