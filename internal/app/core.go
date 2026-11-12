package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/core"
)

func (a *App) coreView(c *ui.Context) {
	a.pageTitle(c, "核心", "Core", func() { ui.Button(c, a.t("刷新", "Refresh")).Disabled(a.busy["local-files"]).OnClick(a.refreshLocal) })
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(18).Children(func() {
			if core.ElevatedCoreSupported() {
				a.card(c, a.t("以管理员身份运行 sing-box", "Run sing-box as administrator"), func() {
					enabled := a.settings.ElevateCore
					if ui.Switch(c, &enabled).Disabled(a.busy["core-start"] || a.busy["core-stop"] || a.coreState.Phase == "running" || a.coreState.Phase == "starting").Label(a.t("以管理员身份运行 sing-box", "Run sing-box as administrator")).Changed() {
						a.settings.ElevateCore = enabled
						a.saveSettings()
						a.replaceCoreController()
					}
					a.label(c, a.t("启动 sing-box 时会显示 Windows 用户帐户控制提示；停止时也会再次请求提权。BoxPilot 本身不会以管理员身份运行。", "Windows User Account Control asks for approval when sing-box starts and again when it stops. BoxPilot itself does not run as administrator."))
				})
			}
			if len(a.cpuCoreGroups) > 1 {
				a.card(c, a.t("CPU 调度", "CPU Scheduling"), func() {
					values := []string{a.t("自动（交由 Windows 调度）", "Automatic (let Windows schedule)")}
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
					if ui.Select(c, &selected, values).FillWidth().Label(a.t("核心类型", "Core type")).Changed() {
						for i, value := range values {
							if value == selected {
								a.cpuCoreChoice = keys[i]
								a.settings.CPUCoreChoice = keys[i]
								a.saveSettings()
								break
							}
						}
					}
					ui.PrimaryButton(c, a.t("应用到本机 sing-box", "Apply to local sing-box")).Disabled(a.busy["cpu-core-selection"] || a.coreState.Phase != "running").OnClick(a.applyCPUCoreChoice)
					a.label(c, a.t("仅影响 BoxPilot 管理的本机进程。CPU Sets 是软性调度偏好，不保证严格绑定；选择会记忆，重启后自动应用。管理员模式下，应用 CPU Sets 会触发一次单独的 UAC 提权操作。", "Only affects the local process managed by BoxPilot. CPU Sets are a soft scheduling preference, not strict affinity; the choice is remembered and reapplied after restart. In administrator mode, applying CPU Sets uses a separate one-shot UAC-elevated action."))
				})
			}
			a.card(c, a.t("服务", "Service"), func() {
				a.pill(c, a.corePhase(), a.statusColor(a.coreState.Phase))
				a.line(c, a.t("版本", "Version"), a.coreState.Version)
				if a.coreState.Phase == "running" {
					a.line(c, a.t("运行配置", "Running profile"), a.runningProfileName())
					if a.coreProfileID != "" && a.settings.ProfileID != "" && a.settings.ProfileID != a.coreProfileID {
						a.label(c, a.t("已选择其他配置；当前运行不变，下次启动时生效。", "Another profile is selected. The running core is unchanged until the next start."))
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
					ui.PrimaryButton(c, a.t("启动", "Start")).Disabled(a.busy["core-start"] || a.busy["core-stop"] || a.busy["remote-profile-restart"] || a.busy["cpu-core-detection"] || a.coreState.Phase == "running" || a.closing).OnClick(func() { a.startCore(false) })
					ui.Button(c, a.t("停止", "Stop")).Disabled(a.busy["core-stop"] || a.coreState.Phase == "stopped" || a.coreState.Phase == "").OnClick(a.stopCore)
				})
				a.label(c, a.t("仅管理本应用启动的进程。退出应用时停止；隐藏窗口时继续运行。", "Only child processes started here are managed. Quit stops the core; hiding keeps it running."))
			})
			a.coreManagementCard(c)
			a.card(c, a.t("配置", "Profiles"), func() {
				if len(a.profiles) == 0 {
					a.label(c, a.t("尚无配置，请新建、导入或添加远程配置。", "No profiles yet. Create, import, or add a remote profile."))
				}
				for _, profile := range a.profiles {
					a.profileRow(c, profile)
				}
				ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
					ui.Button(c, a.t("新建配置", "New Profile")).OnClick(func() {
						a.profileID = ""
						a.profileType = "local"
						a.profileName = a.t("新建配置", "New Profile")
						a.profileRemoteURL = ""
						a.profileContent = core.DefaultConfig()
						a.profileDirty = false
						a.profileEditor = true
					})
					ui.Button(c, a.t("远程", "Remote")).OnClick(func() { a.newRemoteProfile("", "") })
					ui.Button(c, a.t("从文件导入", "Import from File")).OnClick(a.importProfile)
				})
			})
			if a.coreState.Output != "" {
				a.card(c, a.t("日志", "Logs"), func() { ui.Text(c, a.coreState.Output).Font("monospace").FontSize(11).Selectable().MaxHeight(240) })
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
	ui.Button(c, a.t("取消下载", "Cancel download")).OnClick(func() {
		if cancel := a.jobs["install"]; cancel != nil {
			cancel()
		}
	})
}

func (a *App) corePhase() string {
	switch a.coreState.Phase {
	case "running":
		return a.t("服务正在运行", "Service is Running")
	case "starting":
		return a.t("服务启动中...", "Service starting...")
	case "checking":
		return a.t("正在检查...", "Checking...")
	case "stopping":
		return a.t("服务停止中...", "Service stopping...")
	case "error":
		return a.t("错误", "Error")
	}
	return a.t("服务未启动", "Service not started")
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
			label += " (" + a.t("测试版", "Beta") + ")"
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
			return errors.New("配置文件超过 8 MiB 限制")
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
	a.profileName = a.t("导入配置", "Import Profile")
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
		a.confirmWithButton(a.t("未保存的更改", "Unsaved Changes"), a.t("您有未保存的更改。要放弃它们吗？", "You have unsaved changes. Do you want to discard them?"), a.t("放弃", "Discard"), func() { a.profileEditor = false; a.profileDirty = false })
	} else {
		a.profileEditor = false
	}
}
func (a *App) profileForm(c *ui.Context) {
	width, height := c.Size()
	nameLabel := a.t("名称", "Name")
	if a.profileType == "remote" {
		nameLabel = a.t("本地显示名称", "Local display name")
	}
	dialog := ui.DialogBase(c, &a.profileEditor, func(back, panel ui.Element) {
		animateDialog(back, panel)
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(min(900, width-32)).MaxHeight(min(700, height-32)).Padding(22).Radius(20).Background(a.palette.card).Gap(12)
		if a.profileType != "remote" {
			panel.Height(min(700, height-32))
		}
		ui.Row(c).FillWidth().Children(func() {
			title := a.t("编辑内容", "Edit Content")
			if a.profileType == "remote" {
				title = a.t("编辑配置", "Edit Profile")
			}
			ui.Text(c, title).FontSize(20).Bold().Grow(1)
			a.iconButton(c, "close-editor", a.t("关闭", "Close"), "×", a.closeProfile)
		})
		if a.profileType == "remote" {
			a.field(c, nameLabel, func() {
				if ui.TextInput(c.Key("profile-name"), &a.profileName).Disabled(a.busy["save-profile"]).FillWidth().Placeholder(a.t("留空时使用 URL 主机名", "Defaults to the URL hostname when empty")).Label(a.t("名称", "Name")).Changed() {
					a.profileDirty = true
				}
			})
			urlLabel := a.t("远程配置 URL", "Remote profile URL")
			a.field(c, urlLabel, func() {
				if ui.TextInput(c.Key("profile-remote-url"), &a.profileRemoteURL).Disabled(a.busy["save-profile"]).FillWidth().Placeholder("https://example.com/config.json").Label(a.t("URL", "URL")).Changed() {
					a.profileDirty = true
				}
			})
			autoUpdate := a.profileAutoUpdate
			ui.Checkbox(c, &a.profileAutoUpdate, a.t("自动更新", "Auto Update"))
			if autoUpdate != a.profileAutoUpdate {
				a.profileDirty = true
			}
			a.field(c, a.t("自动更新间隔", "Auto Update Interval"), func() {
				if ui.TextInput(c.Key("profile-update-interval"), &a.profileAutoUpdateInterval).Disabled(a.busy["save-profile"] || !a.profileAutoUpdate).FillWidth().Placeholder(a.t("单位：分钟", "In Minutes")).Changed() {
					a.profileDirty = true
				}
			})
			if !a.profileLastUpdated.IsZero() {
				a.line(c, a.t("最后更新", "Last Updated"), a.profileLastUpdated.Format("2006-01-02 15:04"))
			}
		} else {
			if ui.TextInput(c.Key("profile-name"), &a.profileName).Disabled(a.busy["save-profile"]).FillWidth().Label(nameLabel).Changed() {
				a.profileDirty = true
			}
			if ui.TextArea(c.Key("profile-content-"+a.profileID), &a.profileContent).Disabled(a.busy["save-profile"]).Font("monospace").FontSize(12).Grow(1).FillWidth().Label(a.t("配置", "Configuration")).Changed() {
				a.profileDirty = true
			}
			a.label(c, a.t("配置可能修改 TUN / DNS / 路由 / 监听；本程序不会自动提权。", "Configuration may affect TUN, DNS, routing, and listeners."))
		}
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			if a.profileType != "remote" {
				ui.Button(c, a.t("校验配置", "Check configuration")).Disabled(a.busy["check-profile"] || a.settings.CorePath == "").OnClick(a.checkProfile)
			}
			ui.Spacer(c)
			ui.Button(c, a.t("取消", "Cancel")).OnClick(a.closeProfile)
			saveAction := a.saveProfile
			if a.profileType == "remote" {
				saveAction = a.saveRemoteProfile
			}
			ui.PrimaryButton(c, a.t("保存", "Save")).Disabled(a.busy["save-profile"]).OnClick(saveAction)
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
		a.fail(errors.New(a.t("必填", "Required")))
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
