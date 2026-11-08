package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
)

func writeBus(t *testing.T, dir string, ms ...bus.Message) {
	t.Helper()
	var data []byte
	for _, m := range ms {
		line, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "bus.ndjson"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func askAt(n int64, age time.Duration) bus.Message {
	return bus.Message{N: n, T: time.Now().Add(-age).UTC().Format(time.RFC3339Nano), From: "worker implement", To: "lead", K: Ask, S: "run go test"}
}

func TestOpenAsksSkipsAnsweredAndExpired(t *testing.T) {
	dir := t.TempDir()
	expired := askAt(3, MaxAskWait+time.Hour)
	writeBus(t, dir,
		askAt(1, time.Second),
		askAt(2, time.Second),
		expired,
		bus.Message{N: 4, K: Allow, Ref: "1"},
		bus.Message{N: 5, K: "msg", S: "not an ask"},
	)
	asks, err := OpenAsks(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, m := range asks {
		got = append(got, m.N)
	}
	if want := []int64{2}; !slices.Equal(got, want) {
		t.Fatalf("open asks = %v, want %v", got, want)
	}
}

func TestOpenAsksDenyAnswersToo(t *testing.T) {
	dir := t.TempDir()
	writeBus(t, dir, askAt(1, time.Second), bus.Message{N: 2, K: Deny, Ref: "1"})
	asks, err := OpenAsks(dir)
	if err != nil || len(asks) != 0 {
		t.Fatalf("asks = %v, err = %v; want none", asks, err)
	}
}

func TestOpenAsksOldestFirst(t *testing.T) {
	dir := t.TempDir()
	writeBus(t, dir, askAt(9, time.Second), askAt(4, time.Second), askAt(7, time.Second))
	asks, err := OpenAsks(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, m := range asks {
		got = append(got, m.N)
	}
	if want := []int64{4, 7, 9}; !slices.Equal(got, want) {
		t.Fatalf("open asks = %v, want %v", got, want)
	}
}
