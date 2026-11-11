package app

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
	"github.com/xmdhs/boxpilot/internal/model"
)

var cardIDs = []string{"upload", "download", "status", "connections", "mode"}

func (a *App) cardTitle(id string) string {
	switch id {
	case "upload":
		return a.t("上传", "Upload")
	case "download":
		return a.t("下载", "Download")
	case "status":
		return a.t("状态", "Status")
	case "connections":
		return a.t("连接", "Connections")
	case "mode":
		return a.t("模式", "Mode")
	}
	return id
}
func (a *App) overview(c *ui.Context) {
	a.pageTitle(c, "概述", "Overview", func() {
		a.iconButton(c, "manage-cards", a.t("仪表项目", "Dashboard Items"), "⚙", func() { a.cardDialog = true })
	})
	if a.client == nil {
		a.card(c, "", func() {
			ui.Row(c).Gap(10).Wrap().Children(func() {
				ui.PrimaryButton(c, a.t("核心", "Core")).OnClick(func() { a.page = "core" })
				ui.Button(c, a.t("连接", "Connect")).OnClick(func() { a.editServer("") })
			})
		})
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(18).FillWidth().Children(func() {
			statusLabel := a.t("服务未启动", "Service not started")
			switch a.snapshot.ServiceStatus.GetStatus() {
			case daemon.ServiceStatus_STARTING:
				statusLabel = a.t("服务启动中...", "Service starting...")
			case daemon.ServiceStatus_STARTED:
				statusLabel = a.t("服务正在运行", "Service is Running")
			case daemon.ServiceStatus_STOPPING:
				statusLabel = a.t("服务停止中...", "Service stopping...")
			case daemon.ServiceStatus_FATAL:
				statusLabel = a.t("服务启动失败", "Service failed to start")
			}
			ui.Row(c).Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
				a.pill(c, statusLabel, a.statusColor(a.snapshot.Phase))
				if a.snapshot.StartedAt > 0 {
					a.label(c, a.t("运行时间", "Uptime")+" "+model.FormatDuration(time.Since(time.UnixMilli(a.snapshot.StartedAt))))
					c.After(time.Second)
				}
				ui.Spacer(c)
				a.label(c, a.snapshot.Version)
			})
			order := a.settings.Cards
			if len(order) == 0 {
				order = cardIDs
			}
			ui.Row(c.Key("overview-cards")).Wrap().AlignItems(ui.Stretch).Gap(16).FillWidth().Children(func() {
				for _, id := range order {
					id := id
					if !includes(cardIDs, id) || includes(a.settings.DisabledCards, id) {
						continue
					}
					if id == "mode" && len(a.snapshot.Modes) <= 1 {
						continue
					}
					c.Key("card-" + id)
					card := a.card(c, a.cardTitle(id), func() {
						s := a.snapshot.Status
						switch id {
						case "upload", "download":
							value, total := s.GetUplink(), s.GetUplinkTotal()
							history := a.snapshot.UploadHistory
							if id == "download" {
								value, total = s.GetDownlink(), s.GetDownlinkTotal()
								history = a.snapshot.DownloadHistory
							}
							text := "…"
							sub := "…"
							if s.GetTrafficAvailable() {
								text = model.FormatBytes(uint64(max(0, value))) + "/s"
								sub = model.FormatBytes(uint64(max(0, total)))
							}
							ui.Text(c, text).Font("monospace").FontSize(25).FontWeight(500)
							a.label(c, sub).Font("monospace")
							a.sparkline(c, history)
						case "status":
							a.line(c, a.t("内存", "Memory"), model.FormatMemory(s.GetMemory()))
							a.line(c, "Goroutines", fmt.Sprint(s.GetGoroutines()))
						case "connections":
							a.line(c, a.t("入站", "Inbound"), fmt.Sprint(s.GetConnectionsIn()))
							a.line(c, a.t("出站", "Outbound"), fmt.Sprint(s.GetConnectionsOut()))
						case "mode":
							modes := a.snapshot.Modes
							selected := 0
							for i, mode := range modes {
								if mode == a.snapshot.CurrentMode {
									selected = i
									break
								}
							}
							if len(modes) > 1 && len(modes) <= 3 {
								if ui.Segmented(c.Key("mode-segmented"), &selected, modes...).FillWidth().FillEqually().Label(a.t("Clash 模式", "Clash Mode")).Changed() {
									next := modes[selected]
									a.rpc("mode", func(ctx context.Context, client *api.Client) error { return client.SetClashMode(ctx, next) })
								}
							} else if len(modes) > 3 {
								mode := a.snapshot.CurrentMode
								if ui.Select(c.Key("mode-select"), &mode, modes).FillWidth().Label(a.t("Clash 模式", "Clash Mode")).Changed() {
									next := mode
									a.rpc("mode", func(ctx context.Context, client *api.Client) error { return client.SetClashMode(ctx, next) })
								}
							}
						}
					})
					card.Label(a.t("仪表卡片", "Dashboard card") + ": " + a.cardTitle(id)).Basis(overviewCardWidth).Grow(1).MinWidth(0)
					if id == "mode" {
						card.BasisPercent(100)
					}
				}
			})
			a.unsupported(c, "status")
			a.unsupported(c, "clashMode")
		})
	})
}
func (a *App) cardManager(c *ui.Context) {
	ui.DialogBase(c, &a.cardDialog, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(480).MaxWidthPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(14)
		ui.Text(c, a.t("仪表项目", "Dashboard Items")).FontSize(20).Bold()
		order := a.settings.Cards
		if len(order) == 0 {
			order = append([]string(nil), cardIDs...)
		}
		for i, id := range order {
			i, id := i, id
			if !includes(cardIDs, id) {
				continue
			}
			ui.Row(c.Key(id)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.cardTitle(id)).Grow(1)
				a.iconButton(c, "up-"+id, "↑", "↑", func() {
					if i > 0 {
						next := append([]string(nil), order...)
						next[i-1], next[i] = next[i], next[i-1]
						a.settings.Cards = next
						a.saveSettings()
					}
				})
				a.iconButton(c, "down-"+id, "↓", "↓", func() {
					if i < len(order)-1 {
						next := append([]string(nil), order...)
						next[i+1], next[i] = next[i], next[i+1]
						a.settings.Cards = next
						a.saveSettings()
					}
				})
				on := !includes(a.settings.DisabledCards, id)
				if ui.Switch(c.Key("enabled-"+id), &on).Label(a.cardTitle(id)).Changed() {
					if on {
						a.settings.DisabledCards = remove(a.settings.DisabledCards, id)
					} else {
						a.settings.DisabledCards = append(a.settings.DisabledCards, id)
					}
					a.saveSettings()
				}
			})
		}
		ui.Row(c).Gap(10).FillWidth().Children(func() {
			ui.Button(c, a.t("重置", "Reset")).OnClick(func() {
				a.settings.Cards = append([]string(nil), cardIDs...)
				a.settings.DisabledCards = nil
				a.saveSettings()
			})
			ui.Spacer(c)
			ui.PrimaryButton(c, a.t("完成", "Done")).OnClick(func() { a.cardDialog = false })
		})
	})
}
