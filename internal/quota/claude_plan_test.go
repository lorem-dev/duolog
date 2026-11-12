package quota

import "testing"

func TestParseClaudePlan(t *testing.T) {
	for _, tc := range []struct {
		name, data, plan, source string
		fails                    bool
	}{
		{"ultra", `{"loggedIn":true,"subscriptionType":"max","planTier":"max_20x"}`, "ultra", "claude auth status: subscriptionType max, planTier max_20x", false},
		{"long tier", `{"subscriptionType":"max","planTier":"default_claude_max_20x"}`, "ultra", "claude auth status: subscriptionType max, planTier default_claude_max_20x", false},
		{"max", `{"subscriptionType":"max","planTier":"max_5x"}`, "max", "claude auth status: subscriptionType max, planTier max_5x", false},
		{"pro", `{"subscriptionType":"pro"}`, "pro", "claude auth status: subscriptionType pro", false},
		{"none", `{"loggedIn":true}`, "", "claude auth status: subscriptionType none", false},
		{"odd field", `{"subscriptionType":"Max Plan"}`, "", "claude auth status: subscriptionType unreadable", false},
		{"logged out", `{"loggedIn":false,"subscriptionType":"pro"}`, "", "", true},
		{"not json", `oops`, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseClaudePlan([]byte(tc.data))
			if tc.fails {
				if err == nil {
					t.Fatalf("parseClaudePlan(%s) = %+v, want an error", tc.data, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Plan != tc.plan || got.Source() != tc.source {
				t.Fatalf("parseClaudePlan(%s) = %q, %q; want %q, %q", tc.data, got.Plan, got.Source(), tc.plan, tc.source)
			}
		})
	}
}
