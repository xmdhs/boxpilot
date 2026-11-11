package core

import (
	"reflect"
	"testing"
)

func TestClassifyCPUCoreGroups(t *testing.T) {
	sets := []cpuSetInfo{
		{id: 8, efficiencyClass: 0, schedulingClass: 0}, // LP E-core
		{id: 4, efficiencyClass: 0, schedulingClass: 1}, // E-core
		{id: 2, efficiencyClass: 1, schedulingClass: 1}, // intermediate efficiency class
		{id: 0, efficiencyClass: 2, schedulingClass: 0}, // P-core
	}
	groups := classifyCPUCoreGroups(sets)
	if len(groups) != 3 {
		t.Fatalf("expected three dynamically detected groups, got %#v", groups)
	}
	wantKinds := []string{"performance", "efficiency", "low-power"}
	for i, want := range wantKinds {
		if groups[i].Kind != want {
			t.Errorf("group %d kind = %q, want %q", i, groups[i].Kind, want)
		}
	}
	wantIDs := [][]uint32{{0}, {2, 4}, {8}}
	for i := range wantIDs {
		if !reflect.DeepEqual(groups[i].CPUSetIDs, wantIDs[i]) {
			t.Errorf("group %q CPU set IDs = %v, want %v", groups[i].Kind, groups[i].CPUSetIDs, wantIDs[i])
		}
	}
}

func TestClassifyCPUCoreGroupsWithoutHeterogeneousEfficiency(t *testing.T) {
	sets := []cpuSetInfo{
		{id: 0, efficiencyClass: 1, schedulingClass: 0},
		{id: 1, efficiencyClass: 1, schedulingClass: 1},
	}
	if groups := classifyCPUCoreGroups(sets); len(groups) != 0 {
		t.Fatalf("expected no selectable groups for homogeneous efficiency classes, got %#v", groups)
	}
}
