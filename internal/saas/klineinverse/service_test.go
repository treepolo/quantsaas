package klineinverse

import (
	"testing"

	core "quantsaas/internal/klineinverse"
)

func TestOperationSchedulesPreserveFiniteQuotas(t *testing.T) {
	schedule := operationSchedule(17)
	if len(schedule) != 17 {
		t.Fatalf("len=%d", len(schedule))
	}
	counts := map[string]int{}
	for _, operation := range schedule {
		counts[operation]++
	}
	for _, operation := range core.OperationOrder {
		if counts[operation] == 0 {
			t.Fatalf("operation %s has no work", operation)
		}
	}
	selected := selectedOperationSchedule(7, []string{core.OperationLocal, core.OperationBlock})
	if selected[0] != core.OperationLocal || selected[1] != core.OperationBlock || selected[6] != core.OperationLocal {
		t.Fatalf("selected schedule=%v", selected)
	}
}

func TestPassCurveUsesObservedRadii(t *testing.T) {
	points := []BoundaryPoint{
		{Distance: core.Distance{Total: .1}, QRelative: 1},
		{Distance: core.Distance{Total: .1}, QRelative: -1},
		{Distance: core.Distance{Total: .3}, QRelative: 2},
	}
	curve := passCurve(points, func(point BoundaryPoint) bool { return point.QRelative > 0 })
	if len(curve) != 2 || curve[0].Radius != .1 || curve[0].Passed != 1 || curve[0].Total != 2 || curve[0].Rate != .5 || curve[1].Rate != 2.0/3.0 {
		t.Fatalf("curve=%+v", curve)
	}
}
