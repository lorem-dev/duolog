package calibratetest

import "testing"

func TestTreeGuardContract(t *testing.T) {
	TreeGuardContract(t, func(t *testing.T) (TreeGuard, func(string), string) {
		return &Fake{}, func(string) {}, Refusal
	})
}
