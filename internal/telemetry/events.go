// Package telemetry keeps closed, numeric usage events on this machine only.
// It has no network code and no export.
package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

const schemaVersion = 1

var (
	errBadID       = errors.New("telemetry: invalid id")
	errNoEvent     = errors.New("telemetry: no event")
	errEncode      = errors.New("telemetry: cannot encode event")
	errLineTooLong = errors.New("telemetry: event line over 1024 bytes")
)

// ID is a duolog random id. Only NewID and reading a record make one.
type ID struct{ value string }

// NewID returns 16 random bytes as 32 lowercase hex characters.
func NewID() (ID, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ID{}, err
	}
	return ID{value: hex.EncodeToString(b)}, nil
}

// MarshalJSON writes the id as a string. An empty id is an error, never an
// empty object; events omit it with omitzero.
func (id ID) MarshalJSON() ([]byte, error) {
	if !validID(id.value) {
		return nil, errBadID
	}
	return json.Marshal(id.value)
}

func (id *ID) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) != nil || !validID(s) {
		return errBadID
	}
	id.value = s
	return nil
}

func (id ID) check() error {
	if id.value != "" && !validID(id.value) {
		return errBadID
	}
	return nil
}

func validID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Names are the configured role and class names; any other name is written
// as other.
type Names struct {
	Roles, Classes []string
}

// Event is one of the record types below.
type Event interface{ telemetryEvent() }

// Common is filled by Encode; only the ids come from the caller.
type Common struct {
	V    int    `json:"v"`
	T    int64  `json:"t"`
	Type string `json:"type"`
	Run  ID     `json:"run,omitzero"`
	Task ID     `json:"task,omitzero"`
}

type Run struct {
	Common
	Role       string `json:"role"`
	Class      string `json:"class"`
	Agent      string `json:"agent"`
	Family     string `json:"family"`
	Effort     string `json:"effort"`
	Placement  string `json:"placement"`
	Attempt    int    `json:"attempt"`
	In         int64  `json:"in"`
	Cached     int64  `json:"cached"`
	Out        int64  `json:"out"`
	UsageKnown bool   `json:"usage_known"`
	USDmicro   int64  `json:"usd_micro"`
	Priced     bool   `json:"priced"`
	Secs       int64  `json:"secs"`
	Outcome    string `json:"outcome"`
	Err        string `json:"err"`
	Asks       int64  `json:"asks"`
	Denials    int64  `json:"denials"`
	Escalated  bool   `json:"escalated"`
	Moved      bool   `json:"moved"`
	Reason     string `json:"reason"`
}

type Window struct {
	Kind       string `json:"kind"`
	LenMin     int64  `json:"len_min"`
	UsedBP     int64  `json:"used_bp"`
	ResetInMin int64  `json:"reset_in_min"`
	Family     string `json:"family"`
	Status     string `json:"status"`
}

type Quota struct {
	Common
	Agent        string   `json:"agent"`
	Src          string   `json:"src"`
	Plan         string   `json:"plan"`
	Epoch        int64    `json:"epoch"`
	Windows      []Window `json:"windows"`
	Overage      bool     `json:"overage"`
	SpendControl bool     `json:"spend_control"`
}

type Target struct {
	Agent  string `json:"agent"`
	Family string `json:"family"`
	Effort string `json:"effort"`
}

type Route struct {
	Common
	Role       string   `json:"role"`
	Class      string   `json:"class"`
	Candidates []Target `json:"candidates"`
	Chosen     Target   `json:"chosen"`
	Incumbent  Target   `json:"incumbent"`
	Rule       string   `json:"rule"`
	ScoreMin   float64  `json:"score_min"`
}

type Gate struct {
	Common
	Kind      string `json:"kind"`
	RuleClass string `json:"rule_class"`
	Answer    string `json:"answer"`
	By        string `json:"by"`
	WaitSecs  int64  `json:"wait_secs"`
}

type Peer struct {
	Common
	Op      string `json:"op"`
	Agent   string `json:"agent"`
	Secs    int64  `json:"secs"`
	Outcome string `json:"outcome"`
}

type Reset struct {
	Common
	Agent  string `json:"agent"`
	Reason string `json:"reason"`
}

func (Run) telemetryEvent()   {}
func (Quota) telemetryEvent() {}
func (Route) telemetryEvent() {}
func (Gate) telemetryEvent()  {}
func (Peer) telemetryEvent()  {}
func (Reset) telemetryEvent() {}

// cleaner returns a checked copy with every string in its closed set.
type cleaner interface {
	clean(stamp Common, names Names) (any, error)
}

// Encode returns e as one JSON line ending in a newline. It sets v, t and
// type, writes unknown strings as other, and refuses numbers out of bounds
// and lines over 1024 bytes. The caller's event is not changed.
func Encode(e Event, names Names, now time.Time) ([]byte, error) {
	if e == nil {
		return nil, errNoEvent
	}
	if v := reflect.ValueOf(e); v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, errNoEvent
	}
	c, ok := e.(cleaner)
	if !ok {
		return nil, errNoEvent
	}
	stamp := Common{V: schemaVersion, T: now.Unix()}
	if err := bounded("t", stamp.T, 0, maxSafe); err != nil {
		return nil, err
	}
	value, err := c.clean(stamp, names)
	if err != nil {
		return nil, err
	}
	line, err := json.Marshal(value)
	if err != nil {
		return nil, errEncode
	}
	line = append(line, '\n')
	if len(line) > maxLine {
		return nil, errLineTooLong
	}
	return line, nil
}
