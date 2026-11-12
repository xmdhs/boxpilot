package app

import (
	"context"
	"errors"
	"github.com/xmdhs/boxpilot/locales"

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
	if a.desktop != nil {
		a.desktop.RefreshMenu()
	}
}

func (a *App) profileInUse(id string) bool {
	if a.busy["core-start"] || a.busy["core-stop"] || a.busy["remote-profile-restart"] {
		return true
	}
	return id == a.coreProfileID && (a.coreState.Phase == "running" || a.coreState.Phase == "stopping")
}

func (a *App) deleteProfile(profile core.Profile) {
	if a.profileInUse(profile.ID) {
		a.fail(errors.New(locales.In(a.language()).StopTheCoreBeforeDeletingItsConfiguration()))
		return
	}
	a.confirm(locales.In(a.language()).DeleteProfile(), profile.Name, func() {
		// 菜单和确认框都可能停留一段时间，执行前再次检查实际运行配置。
		if a.profileInUse(profile.ID) {
			a.fail(errors.New(locales.In(a.language()).StopTheCoreBeforeDeletingItsConfiguration()))
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
		selectLabel := locales.In(a.language()).SelectProfile() + ": " + profile.Name
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
							a.pill(c, locales.In(a.language()).Running(), a.palette.good)
						}
						if selected && a.coreState.Phase == "running" && !running {
							a.pill(c, locales.In(a.language()).SelectedNextStart(), a.palette.accent)
						}
						if profile.Type == "remote" {
							updated := locales.In(a.language()).NotUpdatedYet()
							if !profile.LastUpdated.IsZero() {
								updated = profile.LastUpdated.Format("2006-01-02 15:04")
							}
							a.label(c, locales.In(a.language()).Remote()+" · "+updated)
						} else {
							a.label(c, locales.In(a.language()).Local())
						}
					})
				})
			})
		})
		selectButton.OnClick(func() { a.selectProfile(profile) })
		ui.PopoverMenuButton(c.Key("profile-actions-"+profile.ID), locales.In(a.language()).More(), func(m *ui.Menu) {
			m.MaxWidth(260)
			if m.Item(locales.In(a.language()).EditProfile()).Chosen() {
				a.editProfile(profile)
			}
			if profile.Type == "remote" {
				if m.Item(locales.In(a.language()).UpdateProfile()).Disabled(a.busy["remote-update-"+profile.ID] || a.busy["remote-profile-restart"]).Chosen() {
					a.updateRemoteProfile(profile)
				}
			}
			if m.Item(locales.In(a.language()).DuplicateProfile()).Disabled(a.busy["copy-profile"]).Chosen() {
				a.duplicateProfile(profile)
			}
			m.Separator()
			if m.Item(locales.In(a.language()).DeleteProfile()).Danger().Disabled(a.profileInUse(profile.ID) || a.busy["delete-profile"]).Chosen() {
				a.deleteProfile(profile)
			}
		}).Label(locales.In(a.language()).ProfileActions() + ": " + profile.Name)
	})
}

func (a *App) coreManagementCard(c *ui.Context) {
	a.card(c, locales.In(a.language()).CoreManagement(), func() {
		if a.settings.CorePath != "" {
			a.label(c, a.settings.CorePath).SingleLine().Tooltip(a.settings.CorePath)
		} else {
			a.label(c, locales.In(a.language()).SelectAnExistingCoreOrInstallAReleaseFirst())
		}
		ui.Collapsible(c.Key("core-management"), locales.In(a.language()).ExecutableReleases(), &a.coreManagerOpen, func() {
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
	a.field(c, locales.In(a.language()).SelectedExecutable(), func() {
		if ui.Select(c, &a.settings.CorePath, paths).FillWidth().Label(locales.In(a.language()).CorePath()).Changed() {
			a.saveSettings()
		}
	})
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.TextInput(c, &a.externalBinary).FillWidth().MinWidth(0).Placeholder(locales.In(a.language()).AbsolutePathToAnExistingSingBoxBinary()).Label(locales.In(a.language()).ExistingBinaryPath())
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			ui.Button(c, locales.In(a.language()).Browse()).OnClick(a.browseCore)
			ui.Button(c, locales.In(a.language()).VerifyUse()).Disabled(a.busy["inspect"] || tidy(a.externalBinary) == "").OnClick(a.inspectCore)
		})
	})
	ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
		releaseOptions := []string{locales.In(a.language()).Stable(), locales.In(a.language()).Beta()}
		selected := releaseOptions[0]
		if a.releaseChannel == "prerelease" {
			selected = releaseOptions[1]
		}
		if ui.Select(c, &selected, releaseOptions).Width(130).Label(locales.In(a.language()).UpdateTrack()).Changed() {
			a.releaseChannel = "stable"
			if selected == releaseOptions[1] {
				a.releaseChannel = "prerelease"
			}
		}
		ui.Button(c, locales.In(a.language()).CheckUpdate()).Disabled(a.busy["releases"]).OnClick(a.checkReleases)
		if a.busy["releases"] {
			ui.Spinner(c)
		}
	})
	if a.releaseChannel == "prerelease" {
		a.label(c, locales.In(a.language()).PrereleaseBuildsMayBeUnstableReviewTheVersionBeforeInstalling())
	}
	if len(a.releases) > 0 {
		versions := make([]string, len(a.releases))
		for i, release := range a.releases {
			versions[i] = release.Version
			if release.Prerelease {
				versions[i] += " (" + locales.In(a.language()).Beta() + ")"
			}
		}
		if a.releaseChoice == "" || !includes(versions, a.releaseChoice) {
			a.releaseChoice = versions[0]
		}
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Select(c, &a.releaseChoice, versions).FillWidth().Label(locales.In(a.language()).Version())
			ui.PrimaryButton(c, locales.In(a.language()).Install()).AlignSelf(ui.Start).Disabled(a.busy["install"]).OnClick(func() {
				a.confirm(locales.In(a.language()).Install(), locales.In(a.language()).DownloadFromOfficialReleasesVerifySHA256AndInstallTheCoreWillNotStartOrRestartAutomatically()+"\n"+a.releaseChoice, a.installCore)
			})
		})
	}
}
