package app

import (
	"context"
	"fmt"

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

func (a *App) applyCPUCoreChoice() {
	var ids []uint32
	if a.cpuCoreChoice != "auto" {
		found := false
		for _, group := range a.cpuCoreGroups {
			if group.Key == a.cpuCoreChoice {
				ids = append(ids, group.CPUSetIDs...)
				found = true
				break
			}
		}
		if !found {
			a.fail(a.t("核心类型已不可用，请重启 BoxPilot 后重试。", "That CPU core type is no longer available. Restart BoxPilot and try again."))
			return
		}
	}
	manager := a.manager
	if manager == nil {
		a.fail(a.t("本机核心控制器不可用。", "The local core controller is unavailable."))
		return
	}
	a.do("cpu-core-selection", func(ctx context.Context) error {
		return manager.SetCPUSetIDs(ctx, ids)
	}, func() {
		a.notice = a.t("已更新本机 sing-box 的 CPU 调度偏好。", "Updated the CPU scheduling preference for the managed local sing-box.")
	})
}

func (a *App) cpuCoreGroupLabel(group core.CPUCoreGroup) string {
	switch group.Kind {
	case "performance":
		return a.t("性能核（P-core）", "Performance cores (P-core)")
	case "efficiency":
		return a.t("能效核（E-core）", "Efficiency cores (E-core)")
	case "low-power":
		return a.t("低功耗能效核（LP E-core）", "Low-power efficiency cores (LP E-core)")
	default:
		return fmt.Sprintf("%s %d", a.t("核心组 · 效率等级", "Core group · efficiency class"), group.EfficiencyClass)
	}
}
