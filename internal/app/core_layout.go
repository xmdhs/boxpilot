package app

import (
	"context"
	"errors"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/core"
)

func (a *App) runningProfileName() string {
	for _, profile := range a.profiles {
		if profile.ID == a.coreProfileID {
			return profile.Name
		}
	}
	return a.coreProfileID
}

func (a *App) selectProfile(profile core.Profile) {
	a.settings.ProfileID = profile.ID
	a.saveSettings()
}

func (a *App) profileInUse(id string) bool {
	if a.busy["core-start"] || a.busy["core-stop"] || a.busy["remote-profile-restart"] {
		return true
	}
	return id == a.coreProfileID && (a.coreState.Phase == "running" || a.coreState.Phase == "stopping")
}

func (a *App) deleteProfile(profile core.Profile) {
	if a.profileInUse(profile.ID) {
		a.fail(errors.New(a.t("请先停止使用此配置的核心", "Stop the core before deleting its configuration")))
		return
	}
	a.confirm(a.t("删除配置", "Delete Profile"), profile.Name, func() {
		// 菜单和确认框都可能停留一段时间，执行前再次检查实际运行配置。
		if a.profileInUse(profile.ID) {
			a.fail(errors.New(a.t("请先停止使用此配置的核心", "Stop the core before deleting its configuration")))
			return
		}
		a.do("delete-profile", func(context.Context) error {
			return core.DeleteProfile(a.root, profile.ID)
		}, func() {
			if a.settings.ProfileID == profile.ID {
				a.settings.ProfileID = ""
				a.saveSettings()
			}
			a.refreshLocal()
		})
	})
}

func (a *App) profileRow(c *ui.Context, profile core.Profile) {
	selected := profile.ID == a.settings.ProfileID
	running := profile.ID == a.coreProfileID && a.coreState.Phase == "running"
	background := a.palette.card
	border := a.palette.border
	if selected {
		background = a.palette.soft
		border = a.palette.accent.Alpha(.35)
	}

	ui.Row(c.Key("profile-row-"+profile.ID)).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(8).Padding(10).Radius(12).Background(background).Border(1, border).Children(func() {
		selectLabel := a.t("选择配置", "Select Profile") + ": " + profile.Name
		selectButton := ui.ButtonBase(c.Key("profile-select-" + profile.ID)).FillWidth().Grow(1).MinWidth(0).Padding(4).Radius(8).Label(selectLabel).Tooltip(selectLabel)
		if selectButton.Hovered() {
			selectButton.Background(a.palette.hover)
		}
		selectButton.Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(12).Children(func() {
				if selected {
					ui.Text(c, "✓").Width(20).TextColor(a.palette.accent).FontSize(18).Bold().Center()
				} else {
					ui.Box(c).Size(18, 18).Radius(999).Border(1, a.palette.border)
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, profile.Name).SingleLine().Tooltip(profile.Name).Bold()
					ui.Row(c).FillWidth().MinWidth(0).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
						if running {
							a.pill(c, a.t("运行中", "Running"), a.palette.good)
						}
						if selected && a.coreState.Phase == "running" && !running {
							a.pill(c, a.t("已选择 · 下次启动生效", "Selected · next start"), a.palette.accent)
						}
						if profile.Type == "remote" {
							updated := a.t("尚未更新", "Not updated yet")
							if !profile.LastUpdated.IsZero() {
								updated = profile.LastUpdated.Format("2006-01-02 15:04")
							}
							a.label(c, a.t("远程", "Remote")+" · "+updated)
						} else {
							a.label(c, a.t("本地", "Local"))
						}
					})
				})
			})
		})
		selectButton.OnClick(func() { a.selectProfile(profile) })
		ui.PopoverMenuButton(c.Key("profile-actions-"+profile.ID), a.t("更多", "More"), func(m *ui.Menu) {
			m.MaxWidth(260)
			if m.Item(a.t("编辑配置", "Edit Profile")).Chosen() {
				a.editProfile(profile)
			}
			if profile.Type == "remote" {
				if m.Item(a.t("更新配置", "Update Profile")).Disabled(a.busy["remote-update-"+profile.ID] || a.busy["remote-profile-restart"]).Chosen() {
					a.updateRemoteProfile(profile)
				}
			}
			if m.Item(a.t("复制配置", "Duplicate Profile")).Disabled(a.busy["copy-profile"]).Chosen() {
				a.duplicateProfile(profile)
			}
			m.Separator()
			if m.Item(a.t("删除配置", "Delete Profile")).Danger().Disabled(a.profileInUse(profile.ID) || a.busy["delete-profile"]).Chosen() {
				a.deleteProfile(profile)
			}
		}).Label(a.t("配置操作", "Profile actions") + ": " + profile.Name)
	})
}

func (a *App) coreManagementCard(c *ui.Context) {
	a.card(c, a.t("核心管理", "Core Management"), func() {
		if a.settings.CorePath != "" {
			a.label(c, a.settings.CorePath).SingleLine().Tooltip(a.settings.CorePath)
		} else {
			a.label(c, a.t("请先选择已有核心或安装发行版本。", "Select an existing core or install a release first."))
		}
		ui.Collapsible(c.Key("core-management"), a.t("可执行文件与版本安装", "Executable & releases"), &a.coreManagerOpen, func() {
			a.coreBinarySettings(c)
		}).FillWidth()
		if a.busy["install"] {
			a.coreDownloadProgress(c)
		}
	})
}

func (a *App) coreBinarySettings(c *ui.Context) {
	paths := []string{""}
	for _, version := range a.installed {
		paths = append(paths, version.Path)
	}
	if a.settings.CorePath != "" && !includes(paths, a.settings.CorePath) {
		paths = append(paths, a.settings.CorePath)
	}
	a.field(c, a.t("选用的可执行文件", "Selected executable"), func() {
		if ui.Select(c, &a.settings.CorePath, paths).FillWidth().Label(a.t("核心路径", "Core path")).Changed() {
			a.saveSettings()
		}
	})
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.TextInput(c, &a.externalBinary).FillWidth().MinWidth(0).Placeholder(a.t("已有 sing-box 可执行文件的绝对路径", "Absolute path to an existing sing-box binary")).Label(a.t("已有核心路径", "Existing binary path"))
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			ui.Button(c, a.t("选择文件", "Browse")).OnClick(a.browseCore)
			ui.Button(c, a.t("验证并使用", "Verify & use")).Disabled(a.busy["inspect"] || tidy(a.externalBinary) == "").OnClick(a.inspectCore)
		})
	})
	ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
		releaseOptions := []string{a.t("稳定版", "Stable"), a.t("测试版", "Beta")}
		selected := releaseOptions[0]
		if a.releaseChannel == "prerelease" {
			selected = releaseOptions[1]
		}
		if ui.Select(c, &selected, releaseOptions).Width(130).Label(a.t("更新通道", "Update Track")).Changed() {
			a.releaseChannel = "stable"
			if selected == releaseOptions[1] {
				a.releaseChannel = "prerelease"
			}
		}
		ui.Button(c, a.t("检查更新", "Check Update")).Disabled(a.busy["releases"]).OnClick(a.checkReleases)
		if a.busy["releases"] {
			ui.Spinner(c)
		}
	})
	if a.releaseChannel == "prerelease" {
		a.label(c, a.t("预发布版本可能不稳定；安装前请核对版本。", "Prerelease builds may be unstable. Review the version before installing."))
	}
	if len(a.releases) > 0 {
		versions := make([]string, len(a.releases))
		for i, release := range a.releases {
			versions[i] = release.Version
			if release.Prerelease {
				versions[i] += " (" + a.t("测试版", "Beta") + ")"
			}
		}
		if a.releaseChoice == "" || !includes(versions, a.releaseChoice) {
			a.releaseChoice = versions[0]
		}
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Select(c, &a.releaseChoice, versions).FillWidth().Label(a.t("版本", "Version"))
			ui.PrimaryButton(c, a.t("安装", "Install")).AlignSelf(ui.Start).Disabled(a.busy["install"]).OnClick(func() {
				a.confirm(a.t("安装", "Install"), a.t("将从官方发行源下载、验证 SHA-256 并安装此版本；不会自动启动或重启核心。", "Download from official releases, verify SHA-256, and install. The core will not start or restart automatically.")+"\n"+a.releaseChoice, a.installCore)
			})
		})
	}
}
