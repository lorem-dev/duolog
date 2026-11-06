package wiring_test

import (
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/lorem-dev/duolog/internal/wiring/wiringtest"
)

// Cmd fills every field except RunDelegate, which stays nil to run
// delegate.Run on the Delegate of the moment.
func TestCmdFillsEveryField(t *testing.T) {
	for name, d := range map[string]wiring.CmdDeps{"Cmd": wiring.Cmd(), "wiringtest.Cmd": wiringtest.Cmd(t)} {
		v := reflect.ValueOf(d)
		for i := range v.NumField() {
			field := v.Type().Field(i).Name
			if field == "RunDelegate" {
				continue
			}
			if v.Field(i).IsZero() {
				t.Errorf("%s leaves %s empty", name, field)
			}
		}
	}
}
