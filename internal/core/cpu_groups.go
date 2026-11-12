package core

import (
	"fmt"
	"sort"
	"strconv"
)

type cpuSetInfo struct {
	id                    uint32
	group                 uint16
	logicalProcessorIndex uint8
	efficiencyClass       uint8
	schedulingClass       uint8
}

// processorAffinityMask maps selected Windows CPU Set IDs to a strict affinity
// mask. SetProcessAffinityMask accepts a mask within one processor group only.
func processorAffinityMask(ids []uint32, sets []cpuSetInfo) (uintptr, error) {
	byID := make(map[uint32]cpuSetInfo, len(sets))
	for _, set := range sets {
		byID[set.id] = set
	}
	var mask uintptr
	var group uint16
	hasGroup := false
	for _, id := range ids {
		set, ok := byID[id]
		if !ok {
			return 0, fmt.Errorf("CPU Set ID %d is no longer available", id)
		}
		if hasGroup && set.group != group {
			return 0, fmt.Errorf("selected processors span multiple Windows processor groups; strict affinity cannot be applied")
		}
		group, hasGroup = set.group, true
		if int(set.logicalProcessorIndex) >= strconv.IntSize {
			return 0, fmt.Errorf("logical processor index %d exceeds the affinity mask width", set.logicalProcessorIndex)
		}
		mask |= uintptr(1) << set.logicalProcessorIndex
	}
	if mask == 0 {
		return 0, fmt.Errorf("selected processor affinity is empty")
	}
	return mask, nil
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
