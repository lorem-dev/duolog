package budget

import "testing"

func TestPlanStep(t *testing.T) {
	next := map[string]string{"propose": "", "mine": "plan"}
	cases := []struct {
		name, step string
		next       map[string]string
		want       string
	}{
		{"no step", "", next, "run duolog calibrate mine first"},
		{"survey", "survey", next, "run duolog calibrate mine first"},
		{"classes", "classes", next, "run duolog calibrate mine first"},
		{"mine", "mine", next, ""},
		{"judge", "judge", NextStep, ""},
		{"propose", "propose", NextStep, "calibration run cal-1 is at step propose; next: duolog calibrate survey --new"},
		{"propose with a next", "propose", map[string]string{"propose": "survey"}, "calibration run cal-1 is at step propose; next: duolog calibrate survey"},
	}
	for _, tc := range cases {
		err := PlanStep("cal-1", tc.step, tc.next)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s: PlanStep(%q) = %q, want %q", tc.name, tc.step, got, tc.want)
		}
	}
}

func TestCheckApproval(t *testing.T) {
	good := Plan{Run: "cal-1", Classes: []ClassPlan{{Class: "implement"}}}
	good.Hash = good.ComputeHash()
	edited := good
	edited.CapWeekly = 99
	cases := []struct {
		name              string
		approve, approved string
		p                 Plan
		want              string
	}{
		{"already approved", good.Hash, good.Hash, good, "plan " + good.Hash + " is already approved; see duolog calibrate status"},
		{"empty hash", "abc", "", Plan{}, "plan hash does not match; run duolog calibrate plan again"},
		{"mismatch", "abc", "", good, "plan hash does not match; run duolog calibrate plan again"},
		{"edited plan", good.Hash, "", edited, "plan hash does not match; run duolog calibrate plan again"},
		{"good", good.Hash, "", good, ""},
		{"good after another approval", good.Hash, "other", good, ""},
	}
	for _, tc := range cases {
		err := CheckApproval(tc.approve, tc.approved, tc.p)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s: CheckApproval = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestApprove(t *testing.T) {
	ran := Cell{ID: "c001", State: CellDone}
	pending := Cell{ID: "c002", State: CellPending}
	cases := []struct {
		name  string
		step  string
		cells []Cell
		want  Approval
	}{
		{"first approval, no cells", "plan", nil, Approval{Step: "plan", Next: "run"}},
		{"first approval, pending cells", "mine", []Cell{pending}, Approval{Step: "plan", Next: "run"}},
		{"after run, all cells ran", "run", []Cell{ran}, Approval{Step: "run", Next: "judge", Kept: 1}},
		{"after judge, no cells", "judge", nil, Approval{Step: "run", Next: "judge"}},
		{"after run, one kept and one pending", "run", []Cell{ran, pending}, Approval{Step: "plan", Next: "resume", Kept: 1}},
		{"before run, one kept cell", "plan", []Cell{ran}, Approval{Step: "plan", Next: "resume", Kept: 1}},
	}
	for _, tc := range cases {
		if got := Approve(tc.step, tc.cells); got != tc.want {
			t.Errorf("%s: Approve(%q) = %+v, want %+v", tc.name, tc.step, got, tc.want)
		}
	}
}

func TestChoosePlanSave(t *testing.T) {
	cases := []struct {
		name         string
		approvedHash string
		approved     bool
		hash         string
		want         PlanSave
	}{
		{"no approval", "", false, "h1", SavePlan},
		{"approved another plan", "h0", true, "h1", SaveNextPlan},
		{"approved this plan", "h1", true, "h1", RemoveNextPlan},
	}
	for _, tc := range cases {
		if got := ChoosePlanSave(tc.approvedHash, tc.approved, tc.hash); got != tc.want {
			t.Errorf("%s: ChoosePlanSave = %d, want %d", tc.name, got, tc.want)
		}
	}
}
