package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"

	"github.com/xmdhs/boxpilot/internal/core"
)

func (a *App) detectCPUCoreGroups() {
	var groups []core.CPUCoreGroup
	a.do("cpu-core-detection", func(context.Context) error {
		groups = core.CPUCoreGroups()
		return nil
	}, func() {
		a.cpuCoreGroups = groups
		if !includesCPUCoreChoice(groups, a.cpuCoreChoice) {
			a.cpuCoreChoice = "auto"
			a.settings.CPUCoreChoice = "auto"
			a.saveSettings()
		}
	})
}

func includesCPUCoreChoice(groups []core.CPUCoreGroup, key string) bool {
	if key == "auto" {
		return true
	}
	for _, group := range groups {
		if group.Key == key {
			return true
		}
	}
	return false
}

func (a *App) selectedCPUSetIDs() ([]uint32, bool) {
	if a.cpuCoreChoice == "auto" || len(a.cpuCoreGroups) == 0 {
		return nil, true
	}
	for _, group := range a.cpuCoreGroups {
		if group.Key == a.cpuCoreChoice {
			return append([]uint32(nil), group.CPUSetIDs...), true
		}
	}
	return nil, false
}

func (a *App) applyCPUCoreChoice() {
	ids, valid := a.selectedCPUSetIDs()
	if !valid {
		a.fail(errors.New(locales.In(a.language()).ThatCPUCoreTypeIsNoLongerAvailableRestartBoxPilotAndTryAgain()))
		return
	}
	manager := a.manager
	if manager == nil {
		a.fail(errors.New(locales.In(a.language()).TheLocalCoreControllerIsUnavailable()))
		return
	}
	a.do("cpu-core-selection", func(ctx context.Context) error {
		return manager.SetCPUAffinity(ctx, ids)
	}, nil)
}

func (a *App) cpuCoreGroupLabel(group core.CPUCoreGroup) string {
	switch group.Kind {
	case "performance":
		return locales.In(a.language()).PerformanceCoresPCore()
	case "efficiency":
		return locales.In(a.language()).EfficiencyCoresECore()
	case "low-power":
		return locales.In(a.language()).LowPowerEfficiencyCoresLPECore()
	default:
		return fmt.Sprintf("%s %d", locales.In(a.language()).CoreGroupEfficiencyClass(), group.EfficiencyClass)
	}
}
