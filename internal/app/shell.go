package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var pages = []struct{ id, zh, en, icon string }{{"overview", "概述", "Overview", "dashboard"}, {"groups", "组", "Groups", "folder"}, {"connections", "连接", "Connections", "swap_vert"}, {"logs", "日志", "Logs", "text_snippet"}, {"tools", "工具", "Tools", "terminal"}, {"settings", "设置", "Settings", "settings"}, {"core", "核心", "Core", "memory"}}

func (a *App) view(c *ui.Context) {
	dark := c.Theme().Dark
	if a.settings.Theme == "light" {
		dark = false
	} else if a.settings.Theme == "dark" {
		dark = true
	}
	a.palette = makePalette(dark, a.settings.Accent)
	c.SetTheme(a.palette.theme)
	width, _ := c.Size()
	compact := width <= compactBreakpoint
	ui.Row(c).Fill().Background(a.palette.bg).Children(func() {
		if !compact {
			a.sidebar(c)
		}
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			a.toolbar(c, compact)
			if a.notice != "" {
				ui.Row(c).FillWidth().Padding(8, 20).Background(a.palette.soft).Gap(12).Children(func() {
					ui.Text(c, a.notice).FontSize(12).Grow(1)
					a.iconButton(c, "dismiss-notice", a.t("关闭", "Close"), "×", func() { a.notice = "" })
				})
			}
			ui.Column(c.Key(a.page)).FillWidth().Grow(1).MinHeight(0).Padding(pagePadding).Gap(pageGap).Children(func() {
				switch a.page {
				case "overview":
					a.overview(c)
				case "groups":
					a.groups(c)
				case "connections":
					a.connections(c)
				case "logs":
					a.logs(c)
				case "tools":
					a.tools(c)
				case "core":
					a.coreView(c)
				case "settings":
					a.settingsView(c)
				}
			})
		})
	})
	if compact && a.compactMenu {
		ui.Overlay(c, func() {
			back := ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Background(ui.RGBA(0, 0, 0, .3)).Modal()
			back.Children(func() {
				panel := ui.Column(c).Width(sidebarWidth).FillHeight().Children(func() { a.sidebar(c) })
				if panel.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) {
					a.compactMenu = false
				}
			})
		})
	}
	a.dialogs(c)
	if a.closing {
		ui.Overlay(c, func() {
			ui.Column(c).Fill().Background(a.palette.bg.Alpha(.95)).Modal().Center().Gap(12).Children(func() {
				ui.Spinner(c)
				ui.Text(c, a.t("服务停止中...", "Service stopping..."))
			})
		})
	}
}

func (a *App) sidebar(c *ui.Context) {
	p := a.palette
	ui.Column(c).Width(sidebarWidth).Shrink(0).FillHeight().Background(p.surface).Padding(16, 12).Gap(6).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
		ui.Column(c).Padding(12, 14, 26).Gap(2).Children(func() {
			ui.Text(c, "BoxPilot").Font("Source Serif 4, Georgia, serif").FontSize(23).FontWeight(560)
		})
		for _, page := range pages {
			page := page
			b := ui.ButtonBase(c.Key(page.id)).FillWidth().Height(38).Radius(10).PaddingX(14).Label(a.t(page.zh, page.en))
			color := p.muted
			if a.page == page.id {
				b.Background(p.soft)
				color = p.accent
			} else if b.Hovered() {
				b.Background(p.hover)
			}
			b.TextColor(color).Children(func() {
				ui.Icon(c, icons[page.icon]).Size(17, 17)
				ui.Text(c, a.t(page.zh, page.en)).FontSize(13).FontWeight(500).Grow(1)
			})
			b.Gap(12).OnClick(func() { a.page = page.id; a.compactMenu = false })
		}
		ui.Spacer(c)
		ui.Column(c).Padding(12).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
			a.label(c, a.activeName()).SingleLine()
			ui.Row(c).Gap(6).Children(func() {
				a.pill(c, a.phaseLabel(), a.statusColor(a.snapshot.Phase))
				if a.snapshot.Version != "" {
					a.label(c, a.snapshot.Version).FontSize(10)
				}
			})
			ui.Button(c, a.t("退出", "Quit")).FillWidth().OnClick(mygo.App.Quit)
		})
	})
}

func (a *App) toolbar(c *ui.Context, compact bool) {
	ui.Row(c).FillWidth().Height(58).PaddingX(20).Gap(12).AlignItems(ui.Center).Background(a.palette.bg).BorderWidth(0, 0, 1, 0).BorderColor(a.palette.border).Children(func() {
		if compact {
			a.iconButton(c, "navigation", a.t("切换导航", "Toggle navigation"), "☰", func() { a.compactMenu = !a.compactMenu })
		}
		choices := []string{a.t("本机", "This Computer")}
		ids := []string{localID}
		for _, s := range a.settings.Servers {
			choices = append(choices, s.Name+" · "+s.ID[:min(6, len(s.ID))])
			ids = append(ids, s.ID)
		}
		choice := choices[0]
		for i, id := range ids {
			if id == a.settings.ActiveID {
				choice = choices[i]
			}
		}
		if ui.Select(c.Key("active-server"), &choice, choices).Grow(1).MinWidth(100).MaxWidth(230).Label(a.t("服务器", "Servers")).Changed() {
			for i, label := range choices {
				if label == choice {
					a.activate(ids[i])
					break
				}
			}
		}
		ui.Spacer(c)
		if a.settings.ActiveID == localID || a.settings.ActiveID == "" {
			label := a.t("启动", "Start")
			if a.coreState.Phase == "running" {
				label = a.t("停止", "Stop")
			}
			ui.Button(c, label).Disabled(a.busy["core-start"] || a.busy["core-stop"]).OnClick(a.toggleCore)
		} else {
			a.iconButton(c, "reconnect", a.t("重试", "Retry"), "↻", func() { a.activate(a.settings.ActiveID) })
		}
		a.iconButton(c, "add-server", a.t("新建服务器", "New Server"), "+", func() { a.editServer("") })
	})
}
func (a *App) activeName() string {
	if a.settings.ActiveID == localID || a.settings.ActiveID == "" {
		return a.t("本机", "This Computer")
	}
	for _, s := range a.settings.Servers {
		if s.ID == a.settings.ActiveID {
			return s.Name
		}
	}
	return "BoxPilot"
}
func (a *App) phaseLabel() string {
	switch a.snapshot.Phase {
	case "active":
		return a.t("已连接", "Connected")
	case "connecting":
		return a.t("连接中...", "Connecting...")
	case "error":
		return a.t("连接失败", "Connection failed")
	}
	return a.t("不可用", "Unavailable")
}
func (a *App) streamBanner(c *ui.Context) {
	if a.snapshot.Error != "" {
		ui.Text(c, a.redactSensitive(a.snapshot.Error)).FontSize(12).TextColor(a.palette.danger).Background(a.palette.danger.Alpha(.08)).Padding(12).Radius(10).FillWidth().Selectable()
	}
	if a.snapshot.Phase == "connecting" {
		ui.Row(c).Gap(10).Children(func() {
			ui.Spinner(c).Size(15, 15)
			a.label(c, a.t("连接中...", "Connecting..."))
		})
	}
}
func (a *App) pageTitle(c *ui.Context, zh, en string, actions func()) {
	if narrowPage(c) {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			a.heading(c, zh, en)
			if actions != nil {
				ui.Row(c).FillWidth().Gap(8).Wrap().Children(actions)
			}
		})
	} else {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			a.heading(c, zh, en)
			ui.Spacer(c)
			if actions != nil {
				ui.Row(c).Gap(8).Wrap().Children(actions)
			}
		})
	}
	a.streamBanner(c)
}
func (a *App) dialogs(c *ui.Context) {
	services := c.Services()
	a.serverForm(c)
	a.profileForm(c)
	a.cardManager(c)
	ui.DialogBase(c.Key("error-dialog"), &a.errorOpen, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(480).MaxWidthPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(16)
		ui.Text(c, a.t("错误", "Error")).FontSize(20).Bold()
		ui.Scroll(c).MaxHeight(300).Children(func() { ui.Text(c, a.errorText).Selectable().Font("monospace").FontSize(12) })
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			ui.Button(c, a.t("复制", "Copy")).OnClick(func() { services.WriteClipboard(a.errorText) })
			ui.PrimaryButton(c, a.t("好", "Ok")).OnClick(func() { a.errorOpen = false })
		})
	})
	ui.DialogBase(c.Key("confirm-dialog"), &a.confirmOpen, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(480).MaxWidthPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(16)
		ui.Text(c, a.confirmTitle).FontSize(20).Bold()
		ui.Text(c, a.confirmMessage)
		ui.Row(c).Justify(ui.End).Gap(10).Children(func() {
			ui.Button(c, a.t("取消", "Cancel")).OnClick(func() { a.confirmOpen = false })
			ui.PrimaryButton(c, a.confirmButtonLabel()).OnClick(func() {
				action := a.confirmAction
				a.confirmOpen = false
				a.confirmAction = nil
				if action != nil {
					action()
				}
			})
		})
	})
}
func (a *App) unsupported(c *ui.Context, stream string) {
	if err := a.snapshot.StreamErrors[stream]; err != "" {
		ui.Text(c, fmt.Sprintf("%s: %s", stream, err)).FontSize(12).TextColor(a.palette.danger).Selectable()
	}
}
func includes(s []string, value string) bool {
	for _, v := range s {
		if v == value {
			return true
		}
	}
	return false
}
func remove(s []string, value string) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v != value {
			out = append(out, v)
		}
	}
	return out
}
func tidy(s string) string { return strings.TrimSpace(s) }
