package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/core"
)

func (a *App) coreView(c *ui.Context) {
	a.pageTitle(c, locales.In(a.language()).Core(), func() {
		ui.Button(c, locales.In(a.language()).Refresh()).Disabled(a.busy["local-files"]).OnClick(a.refreshLocal)
	})
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(18).Children(func() {
			if core.ElevatedCoreSupported() {
				a.card(c, locales.In(a.language()).RunSingBoxAsAdministrator(), func() {
					enabled := a.settings.ElevateCore
					if ui.Switch(c, &enabled).Disabled(a.busy["core-start"] || a.busy["core-stop"] || a.coreState.Phase == "running" || a.coreState.Phase == "starting").Label(locales.In(a.language()).RunSingBoxAsAdministrator()).Changed() {
						a.settings.ElevateCore = enabled
						a.saveSettings()
						a.replaceCoreController()
					}
					a.label(c, locales.In(a.language()).WindowsUserAccountControlAsksForApprovalWhenSingBoxStartsAndAgainWhenItStopsBoxPilotItselfDoesNotRunAsAdministrator())
				})
			}
			if len(a.cpuCoreGroups) > 1 {
				a.card(c, locales.In(a.language()).CpuScheduling(), func() {
					values := []string{locales.In(a.language()).AutomaticLetWindowsSchedule()}
					keys := []string{"auto"}
					for _, group := range a.cpuCoreGroups {
						values = append(values, a.cpuCoreGroupLabel(group))
						keys = append(keys, group.Key)
					}
					selected := values[0]
					for i, key := range keys {
						if key == a.cpuCoreChoice {
							selected = values[i]
							break
						}
					}
					if ui.Select(c, &selected, values).FillWidth().Label(locales.In(a.language()).CoreType()).Changed() {
						for i, value := range values {
							if value == selected {
								a.cpuCoreChoice = keys[i]
								a.settings.CPUCoreChoice = keys[i]
								a.saveSettings()
								break
							}
						}
					}
					ui.PrimaryButton(c, locales.In(a.language()).ApplyToLocalSingBox()).Disabled(a.busy["cpu-core-selection"] || a.coreState.Phase != "running").OnClick(a.applyCPUCoreChoice)
					a.label(c, locales.In(a.language()).OnlyAffectsTheLocalProcessManagedByBoxPilotCPUSetsAreASoftSchedulingPreferenceNotStrictAffinityTheChoiceIsRememberedAndReappliedAfterRestartInAdministratorModeApplyingCPUSetsUsesASeparateOneShotUACElevatedAction())
				})
			}
			a.card(c, locales.In(a.language()).Service(), func() {
				a.pill(c, a.corePhase(), a.statusColor(a.coreState.Phase))
				a.line(c, locales.In(a.language()).Version(), a.coreState.Version)
				if a.coreState.Phase == "running" {
					a.line(c, locales.In(a.language()).RunningProfile(), a.runningProfileName())
					if a.coreProfileID != "" && a.settings.ProfileID != "" && a.settings.ProfileID != a.coreProfileID {
						a.label(c, locales.In(a.language()).AnotherProfileIsSelectedTheRunningCoreIsUnchangedUntilTheNextStart())
					}
				}
				if a.coreState.PID > 0 {
					a.line(c, "PID", fmt.Sprint(a.coreState.PID))
				}
				if a.coreState.URL != "" {
					a.line(c, "API", a.coreState.URL)
				}
				if a.coreState.Error != "" {
					ui.Text(c, a.coreState.Error).TextColor(a.palette.danger).FontSize(12).Selectable()
				}
				ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
					ui.PrimaryButton(c, locales.In(a.language()).Start()).Disabled(a.busy["core-start"] || a.busy["core-stop"] || a.busy["remote-profile-restart"] || a.busy["cpu-core-detection"] || a.coreState.Phase == "running" || a.closing).OnClick(func() { a.startCore(false) })
					ui.Button(c, locales.In(a.language()).Stop()).Disabled(a.busy["core-stop"] || a.coreState.Phase == "stopped" || a.coreState.Phase == "").OnClick(a.stopCore)
				})
				a.label(c, locales.In(a.language()).OnlyChildProcessesStartedHereAreManagedQuitStopsTheCoreHidingKeepsItRunning())
			})
			a.coreManagementCard(c)
			if a.coreState.Output != "" {
				a.card(c, locales.In(a.language()).Logs(), func() { ui.Text(c, a.coreState.Output).Font("monospace").FontSize(11).Selectable().MaxHeight(240) })
			}
		})
	})
}
func (a *App) coreDownloadProgress(c *ui.Context) {
	progress := float64(-1)
	if a.downloadTotal > 0 {
		progress = float64(a.downloadDone) / float64(a.downloadTotal)
	}
	ui.Progress(c, progress).FillWidth()
	a.label(c, fmt.Sprintf("%d / %d bytes", a.downloadDone, a.downloadTotal))
	ui.Button(c, locales.In(a.language()).CancelDownload()).OnClick(func() {
		if cancel := a.jobs["install"]; cancel != nil {
			cancel()
		}
	})
}

func (a *App) corePhase() string {
	switch a.coreState.Phase {
	case "running":
		return locales.In(a.language()).ServiceIsRunning()
	case "starting":
		return locales.In(a.language()).ServiceStarting()
	case "checking":
		return locales.In(a.language()).Checking()
	case "stopping":
		return locales.In(a.language()).ServiceStopping()
	case "error":
		return locales.In(a.language()).Error()
	}
	return locales.In(a.language()).ServiceNotStarted()
}
func (a *App) checkReleases() {
	var releases []core.Release
	include := a.releaseChannel == "prerelease"
	a.do("releases", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		var err error
		releases, err = core.Releases(ctx, include)
		return err
	}, func() {
		a.releases = releases
		a.releaseChoice = ""
	})
}
func (a *App) installCore() {
	var selected core.Release
	found := false
	for _, r := range a.releases {
		label := r.Version
		if r.Prerelease {
			label += " (" + locales.In(a.language()).Beta() + ")"
		}
		if label == a.releaseChoice {
			selected = r
			found = true
			break
		}
	}
	if !found {
		return
	}
	a.downloadDone = 0
	a.downloadTotal = selected.Size
	var installed core.Installed
	a.do("install", func(ctx context.Context) error {
		var err error
		lastProgress := time.Time{}
		installed, err = core.Install(ctx, a.root, selected, func(done, total int64) {
			if time.Since(lastProgress) < 100*time.Millisecond && done != total {
				return
			}
			lastProgress = time.Now()
			a.post(func() { a.downloadDone = done; a.downloadTotal = total })
		})
		return err
	}, func() {
		a.settings.CorePath = installed.Path
		a.saveSettings()
		a.refreshLocal()
	})
}
func (a *App) browseCore() {
	parent := a.win
	var paths []string
	a.do("browse-core", func(context.Context) error {
		var err error
		paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: "Open"})
		return err
	}, func() {
		if len(paths) > 0 {
			a.externalBinary = paths[0]
		}
	})
}
func (a *App) inspectCore() {
	path := tidy(a.externalBinary)
	var installed core.Installed
	a.do("inspect", func(ctx context.Context) error { var err error; installed, err = core.Inspect(ctx, path); return err }, func() {
		a.settings.CorePath = installed.Path
		a.saveSettings()
	})
}
func (a *App) editProfile(p core.Profile) {
	var content string
	a.do("read-profile", func(context.Context) error { var err error; content, err = core.ReadProfile(p); return err }, func() {
		a.profileID = p.ID
		a.profileType = p.Type
		a.profileName = p.Name
		a.profileRemoteURL = p.RemoteURL
		a.profileAutoUpdate = p.AutoUpdate
		interval := p.AutoUpdateInterval
		if interval <= 0 {
			interval = defaultRemoteUpdateMinutes
		}
		a.profileAutoUpdateInterval = strconv.Itoa(interval)
		a.profileLastUpdated = p.LastUpdated
		a.profileContent = content
		a.profileDirty = false
		a.profileEditor = true
	})
}
func (a *App) duplicateProfile(p core.Profile) {
	a.do("copy-profile", func(context.Context) error {
		text, err := core.ReadProfile(p)
		if err != nil {
			return err
		}
		_, err = core.SaveProfile(a.root, "", p.Name+" (copy)", text)
		return err
	}, a.refreshLocal)
}
func (a *App) importProfile() {
	parent := a.win
	var paths []string
	var text string
	a.do("import-profile", func(context.Context) error {
		var err error
		paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: "Import from File", Filters: []mygo.FileFilter{{Name: "JSON / JSONC", Extensions: []string{"json", "jsonc"}}}})
		if err != nil || len(paths) == 0 {
			return err
		}
		info, err := os.Stat(paths[0])
		if err != nil {
			return err
		}
		if info.Size() > 8<<20 {
			return errors.New(locales.In(a.language()).ConfigurationFileTooLarge())
		}
		data, err := os.ReadFile(paths[0])
		text = string(data)
		return err
	}, func() {
		if len(paths) > 0 {
			a.openImportedProfile(text)
		}
	})
}

func (a *App) openImportedProfile(text string) {
	a.profileID = ""
	a.profileType = "local"
	a.profileName = locales.In(a.language()).ImportProfile()
	a.profileRemoteURL = ""
	a.profileAutoUpdate = false
	a.profileAutoUpdateInterval = ""
	a.profileLastUpdated = time.Time{}
	a.profileContent = text
	a.profileDirty = true
	a.profileEditor = true
}

func (a *App) closeProfile() {
	if a.busy["save-profile"] {
		return
	}
	if a.profileDirty {
		a.confirmWithButton(locales.In(a.language()).UnsavedChanges(), locales.In(a.language()).YouHaveUnsavedChangesDoYouWantToDiscardThem(), locales.In(a.language()).Discard(), func() { a.profileEditor = false; a.profileDirty = false })
	} else {
		a.profileEditor = false
	}
}
func (a *App) profileForm(c *ui.Context) {
	width, height := c.Size()
	nameLabel := locales.In(a.language()).Name()
	if a.profileType == "remote" {
		nameLabel = locales.In(a.language()).LocalDisplayName()
	}
	dialog := ui.DialogBase(c, &a.profileEditor, func(back, panel ui.Element) {
		animateDialog(back, panel)
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(min(900, width-32)).MaxHeight(min(700, height-32)).Padding(22).Radius(20).Background(a.palette.card).Gap(12)
		if a.profileType != "remote" {
			panel.Height(min(700, height-32))
		}
		ui.Row(c).FillWidth().Children(func() {
			title := locales.In(a.language()).EditContent()
			if a.profileType == "remote" {
				title = locales.In(a.language()).EditProfile()
			}
			ui.Text(c, title).FontSize(20).Bold().Grow(1)
			a.iconButton(c, "close-editor", locales.In(a.language()).Close(), "×", a.closeProfile)
		})
		if a.profileType == "remote" {
			a.field(c, nameLabel, func() {
				if ui.TextInput(c.Key("profile-name"), &a.profileName).Disabled(a.busy["save-profile"]).FillWidth().Placeholder(locales.In(a.language()).DefaultsToTheURLHostnameWhenEmpty()).Label(locales.In(a.language()).Name()).Changed() {
					a.profileDirty = true
				}
			})
			urlLabel := locales.In(a.language()).RemoteProfileURL()
			a.field(c, urlLabel, func() {
				if ui.TextInput(c.Key("profile-remote-url"), &a.profileRemoteURL).Disabled(a.busy["save-profile"]).FillWidth().Placeholder("https://example.com/config.json").Label(locales.In(a.language()).Url()).Changed() {
					a.profileDirty = true
				}
			})
			autoUpdate := a.profileAutoUpdate
			ui.Checkbox(c, &a.profileAutoUpdate, locales.In(a.language()).AutoUpdate())
			if autoUpdate != a.profileAutoUpdate {
				a.profileDirty = true
			}
			a.field(c, locales.In(a.language()).AutoUpdateInterval(), func() {
				if ui.TextInput(c.Key("profile-update-interval"), &a.profileAutoUpdateInterval).Disabled(a.busy["save-profile"] || !a.profileAutoUpdate).FillWidth().Placeholder(locales.In(a.language()).InMinutes()).Changed() {
					a.profileDirty = true
				}
			})
			if !a.profileLastUpdated.IsZero() {
				a.line(c, locales.In(a.language()).LastUpdated(), a.profileLastUpdated.Format("2006-01-02 15:04"))
			}
		} else {
			if ui.TextInput(c.Key("profile-name"), &a.profileName).Disabled(a.busy["save-profile"]).FillWidth().Label(nameLabel).Changed() {
				a.profileDirty = true
			}
			if ui.TextArea(c.Key("profile-content-"+a.profileID), &a.profileContent).Disabled(a.busy["save-profile"]).Font("monospace").FontSize(12).Grow(1).FillWidth().Label(locales.In(a.language()).Configuration()).Changed() {
				a.profileDirty = true
			}
			a.label(c, locales.In(a.language()).ConfigurationMayAffectTUNDNSRoutingAndListeners())
		}
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			if a.profileType != "remote" {
				ui.Button(c, locales.In(a.language()).CheckConfiguration()).Disabled(a.busy["check-profile"] || a.settings.CorePath == "").OnClick(a.checkProfile)
			}
			ui.Spacer(c)
			ui.Button(c, locales.In(a.language()).Cancel()).OnClick(a.closeProfile)
			saveAction := a.saveProfile
			if a.profileType == "remote" {
				saveAction = a.saveRemoteProfile
			}
			ui.PrimaryButton(c, locales.In(a.language()).Save()).Disabled(a.busy["save-profile"]).OnClick(saveAction)
		})
	})
	if dialog.Dismissed() {
		if a.busy["save-profile"] {
			a.profileEditor = true
		} else if a.profileDirty {
			a.profileEditor = true
			a.closeProfile()
		}
	}
}

func (a *App) saveProfile() {
	id, name, text := a.profileID, tidy(a.profileName), a.profileContent
	if name == "" {
		a.fail(errors.New(locales.In(a.language()).Required()))
		return
	}
	var profile core.Profile
	a.do("save-profile", func(context.Context) error {
		var err error
		profile, err = core.SaveProfileWithMetadata(a.root, core.Profile{ID: id, Type: "local"}, name, text)
		return err
	}, func() {
		a.profileDirty = false
		a.profileEditor = false
		a.settings.ProfileID = profile.ID
		a.saveSettings()
		a.refreshLocal()
	})
}
func (a *App) checkProfile() {
	binary, text := a.settings.CorePath, a.profileContent
	a.do("check-profile", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		dir, err := os.MkdirTemp("", "boxpilot-check-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		path := dir + string(os.PathSeparator) + "config.json"
		if err = os.WriteFile(path, []byte(text), 0600); err != nil {
			return err
		}
		_, err = core.Check(ctx, binary, path)
		return err
	}, nil)
}

var _ = strings.TrimSpace
