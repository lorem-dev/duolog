package hooktest

import "testing"

func TestEventsContract(t *testing.T) {
	EventsContract(t, func(*testing.T) Events { return &Fake{} })
}
