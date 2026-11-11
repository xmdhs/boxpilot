package core

import "sort"

type cpuSetInfo struct {
	id              uint32
	efficiencyClass uint8
	schedulingClass uint8
}

func classifyCPUCoreGroups(sets []cpuSetInfo) []CPUCoreGroup {
	if len(sets) == 0 {
		return nil
	}
	minClass, maxClass := sets[0].efficiencyClass, sets[0].efficiencyClass
	for _, set := range sets {
		if set.efficiencyClass < minClass {
			minClass = set.efficiencyClass
		}
		if set.efficiencyClass > maxClass {
			maxClass = set.efficiencyClass
		}
	}
	if minClass == maxClass {
		return nil
	}
	minHasDefault, minHasNonDefault := false, false
	for _, set := range sets {
		if set.efficiencyClass != minClass {
			continue
		}
		if set.schedulingClass == 0 {
			minHasDefault = true
		} else {
			minHasNonDefault = true
		}
	}

	groups := map[string]*CPUCoreGroup{}
	for _, set := range sets {
		kind := "efficiency"
		switch {
		case set.efficiencyClass == maxClass:
			kind = "performance"
		case set.efficiencyClass == minClass && minHasDefault && minHasNonDefault && set.schedulingClass == 0:
			kind = "low-power"
		}
		group := groups[kind]
		if group == nil {
			group = &CPUCoreGroup{Key: kind, Kind: kind, EfficiencyClass: set.efficiencyClass}
			groups[kind] = group
		}
		group.CPUSetIDs = append(group.CPUSetIDs, set.id)
	}

	result := make([]CPUCoreGroup, 0, len(groups))
	for _, kind := range []string{"performance", "efficiency", "low-power"} {
		if group := groups[kind]; group != nil {
			sort.Slice(group.CPUSetIDs, func(i, j int) bool { return group.CPUSetIDs[i] < group.CPUSetIDs[j] })
			result = append(result, *group)
		}
	}
	return result
}
