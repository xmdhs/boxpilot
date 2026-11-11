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
					a.label(c, a.t("每次启动 sing-box 时都会显示 Windows 用户帐户控制提示。BoxPilot 本身不会以管理员身份运行。", "Windows User Account Control asks for approval each time sing-box starts. BoxPilot itself does not run as administrator."))
				})
			}
			a.card(c, a.t("服务", "Service"), func() {
				a.pill(c, a.corePhase(), a.statusColor(a.coreState.Phase))
				a.line(c, a.t("版本", "Version"), a.coreState.Version)
				if a.coreState.PID > 0 {
					a.line(c, "PID", fmt.Sprint(a.coreState.PID))
				}
				if a.coreState.URL != "" {
					a.line(c, "API", a.coreState.URL)
				}
				if a.coreState.Error != "" {
					ui.Text(c, a.coreState.Error).TextColor(a.palette.danger).FontSize(12).Selectable()
				}
				ui.Row(c).Gap(10).Children(func() {
					ui.PrimaryButton(c, a.t("开始", "Start")).Disabled(a.busy["core-start"] || a.coreState.Phase == "running" || a.closing).OnClick(func() { a.startCore(false) })
					ui.Button(c, a.t("停止", "Stop")).Disabled(a.busy["core-stop"] || a.coreState.Phase == "stopped" || a.coreState.Phase == "").OnClick(a.stopCore)
				})
				a.label(c, a.t("仅管理本应用启动的进程。退出应用时停止；隐藏窗口时继续运行。", "Only child processes started here are managed. Quit stops the core; hiding keeps it running."))
			})
			a.card(c, a.t("核心", "Core"), func() {
				paths := []string{""}
				for _, v := range a.installed {
					paths = append(paths, v.Path)
				}
				if a.settings.CorePath != "" && !includes(paths, a.settings.CorePath) {
					paths = append(paths, a.settings.CorePath)
				}
				a.field(c, a.t("选用的可执行文件", "Selected executable"), func() {
					if ui.Select(c, &a.settings.CorePath, paths).FillWidth().Label(a.t("核心路径", "Core path")).Changed() {
						a.saveSettings()
					}
				})
				ui.Row(c).Gap(10).FillWidth().Children(func() {
					ui.TextInput(c, &a.externalBinary).Grow(1).Placeholder(a.t("已有 sing-box 可执行文件的绝对路径", "Absolute path to an existing sing-box binary")).Label(a.t("已有核心路径", "Existing binary path"))
					ui.Button(c, a.t("选择文件", "Browse")).OnClick(a.browseCore)
					ui.Button(c, a.t("验证并使用", "Verify & use")).Disabled(a.busy["inspect"] || tidy(a.externalBinary) == "").OnClick(a.inspectCore)
				})
				ui.Row(c).Gap(10).Children(func() {
					releaseValues := []string{"stable", "prerelease"}
					releaseOptions := []string{a.t("稳定版", "Stable"), a.t("测试版", "Beta")}
					releaseOption := releaseOptions[0]
					if a.releaseChannel == "prerelease" {
						releaseOption = releaseOptions[1]
					}
					if ui.Select(c, &releaseOption, releaseOptions).Width(130).Label(a.t("更新通道", "Update Track")).Changed() {
						if releaseOption == releaseOptions[1] {
							a.releaseChannel = releaseValues[1]
						} else {
							a.releaseChannel = releaseValues[0]
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
					for i, r := range a.releases {
						versions[i] = r.Version
						if r.Prerelease {
							versions[i] += " (" + a.t("测试版", "Beta") + ")"
						}
					}
					if a.releaseChoice == "" || !includes(versions, a.releaseChoice) {
						a.releaseChoice = versions[0]
					}
					ui.Row(c).Gap(10).Children(func() {
						ui.Select(c, &a.releaseChoice, versions).Grow(1).Label(a.t("版本", "Version"))
						ui.PrimaryButton(c, a.t("安装", "Install")).Disabled(a.busy["install"]).OnClick(func() {
							a.confirm(a.t("安装", "Install"), a.t("将从官方发行源下载、验证 SHA-256 并安装此版本；不会自动启动或重启核心。", "Download from official releases, verify SHA-256, and install. The core will not start or restart automatically.")+"\n"+a.releaseChoice, a.installCore)
						})
					})
				}
				if a.busy["install"] {
					progress := float64(-1)
					if a.downloadTotal > 0 {
						progress = float64(a.downloadDone) / float64(a.downloadTotal)
					}
					ui.Progress(c, progress).FillWidth()
					a.label(c, fmt.Sprintf("%d / %d bytes", a.downloadDone, a.downloadTotal))
					ui.Button(c, a.t("取消", "Cancel")).OnClick(func() {
						if cancel := a.jobs["install"]; cancel != nil {
							cancel()
						}
					})
				}
			})
			a.card(c, a.t("配置", "Profiles"), func() {
				for _, p := range a.profiles {
					p := p
					ui.Row(c.Key(p.ID)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						if p.ID == a.settings.ProfileID {
							ui.Icon(c, icons["check"]).Size(15, 15).TextColor(a.palette.accent)
						}
						ui.Column(c).Grow(1).Gap(3).Children(func() {
							ui.Text(c, p.Name).SingleLine()
							if p.Type == "remote" {
								a.label(c, a.t("远程", "Remote")+" · "+p.LastUpdated.Format("2006-01-02 15:04")).SingleLine()
							}
						})
						if p.Type == "remote" {
							ui.Button(c, a.t("更新", "Update")).Disabled(a.busy["remote-update-"+p.ID]).OnClick(func() { a.updateRemoteProfile(p) })
						}
						ui.Button(c, a.t("选择配置", "Select Profile")).OnClick(func() { a.settings.ProfileID = p.ID; a.saveSettings() })
						ui.Button(c, a.t("编辑配置", "Edit Profile")).OnClick(func() { a.editProfile(p) })
						ui.Button(c, a.t("复制", "Copy")).OnClick(func() { a.duplicateProfile(p) })
						ui.Button(c, a.t("删除", "Delete")).TextColor(a.palette.danger).OnClick(func() {
							a.confirm(a.t("删除", "Delete"), p.Name, func() {
								if a.coreState.Phase == "running" && a.settings.ProfileID == p.ID {
									a.fail(errors.New(a.t("请先停止使用此配置的核心", "Stop the core before deleting its configuration")))
									return
								}
								a.do("delete-profile", func(context.Context) error { return core.DeleteProfile(a.root, p.ID) }, func() {
									if a.settings.ProfileID == p.ID {
										a.settings.ProfileID = ""
										a.saveSettings()
									}
									a.refreshLocal()
								})
							})
						})
					})
				}
				ui.Row(c).Gap(10).Children(func() {
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
		if len(releases) == 0 {
			a.notice = a.t("不可用", "Unavailable")
		}
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
		a.notice = a.t("完成", "Done")
	})
}
func (a *App) browseCore() {
	var paths []string
	a.do("browse-core", func(context.Context) error {
		var err error
		paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "Open"})
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
		a.notice = a.t("可用", "Available")
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
	var paths []string
	var text string
	a.do("import-profile", func(context.Context) error {
		var err error
		paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "Import from File", Filters: []mygo.FileFilter{{Name: "JSON / JSONC", Extensions: []string{"json", "jsonc"}}}})
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
			a.profileID = ""
			a.profileName = a.t("导入配置", "Import Profile")
			a.profileContent = text
			a.profileDirty = true
			a.profileEditor = true
		}
	})
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
	dialog := ui.DialogBase(c, &a.profileEditor, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(min(900, width-32)).Height(min(700, height-32)).Padding(22).Radius(20).Background(a.palette.card).Gap(12)
		ui.Row(c).FillWidth().Children(func() {
			title := a.t("编辑内容", "Edit Content")
			if a.profileType == "remote" {
				title = a.t("编辑配置", "Edit Profile")
			}
			ui.Text(c, title).FontSize(20).Bold().Grow(1)
			a.iconButton(c, "close-editor", a.t("关闭", "Close"), "×", a.closeProfile)
		})
		if ui.TextInput(c.Key("profile-name"), &a.profileName).Disabled(a.busy["save-profile"]).FillWidth().Label(a.t("名称", "Name")).Changed() {
			a.profileDirty = true
		}
		if a.profileType == "remote" {
			if ui.TextInput(c.Key("profile-remote-url"), &a.profileRemoteURL).Disabled(a.busy["save-profile"]).FillWidth().Label(a.t("URL", "URL")).Changed() {
				a.profileDirty = true
			}
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
		a.notice = a.t("完成", "Done")
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
	}, func() { a.notice = a.t("完成", "Done") })
}

var _ = strings.TrimSpace
