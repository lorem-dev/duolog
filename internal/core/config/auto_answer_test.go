package config

import "testing"

func TestSafeAutoAnswerRefusesEveryTrustLabel(t *testing.T) {
	for _, answer := range []string{
		"Trust and continue",
		"trust the folder",
		"Yes, I trust this folder",
		"  yes, i TRUST THIS FOLDER  ",
		"Yes, allow Codex to work in this folder without asking for approval",
		"Yes, and don't ask again",
		"Always allow",
		"",
	} {
		if SafeAutoAnswer(answer) {
			t.Errorf("%q was accepted", answer)
		}
	}
	for _, answer := range []string{"Continue without trusting", "No, not now", "Yes"} {
		if !SafeAutoAnswer(answer) {
			t.Errorf("%q was refused", answer)
		}
	}
}
