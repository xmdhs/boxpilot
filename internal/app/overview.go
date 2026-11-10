package app

import (
	"context"
	"fmt"
	"time"

	"boxpilot/internal/api"
	"boxpilot/internal/model"
	"github.com/egoist/mygo/ui"
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
	a.pageTitle(c, "概览", "Overview", func() {
		a.iconButton(c, "manage-cards", a.t("管理卡片", "Dashboard Items"), "⚙", func() { a.cardDialog = true })
	})
	if a.client == nil {
		a.card(c, a.t("欢迎使用 BoxPilot", "Welcome to BoxPilot"), func() {
			a.label(c, a.t("一个独立的原生 sing-box 控制面板，不隶属于上游项目。", "An independent native controller for sing-box, not affiliated with upstream."))
			ui.Row(c).Gap(10).Wrap().Children(func() {
				ui.PrimaryButton(c, a.t("安装与管理本机核心", "Manage local core")).OnClick(func() { a.page = "core" })
				ui.Button(c, a.t("连接已有实例", "Connect to existing instance")).OnClick(func() { a.editServer("") })
			})
		})
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(18).FillWidth().Children(func() {
			statusLabel := fmt.Sprint(a.snapshot.ServiceStatus.GetStatus())
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				a.pill(c, statusLabel, a.statusColor(a.snapshot.Phase))
				if a.snapshot.StartedAt > 0 {
					a.label(c, a.t("运行时间 ", "Uptime ")+model.FormatDuration(time.Since(time.UnixMilli(a.snapshot.StartedAt))))
					c.After(time.Second)
				}
				ui.Spacer(c)
				a.label(c, a.snapshot.Version)
			})
			width, _ := c.Size()
			cols := 2
			if width < 820 {
				cols = 1
			}
			order := a.settings.Cards
			if len(order) == 0 {
				order = cardIDs
			}
			ui.Grid(c).Columns(cols).Gap(16).FillWidth().Children(func() {
				for _, id := range order {
					id := id
					if !includes(cardIDs, id) || includes(a.settings.DisabledCards, id) {
						continue
					}
					if id == "mode" && len(a.snapshot.Modes) <= 1 {
						continue
					}
					c.Key("card-" + id)
					a.card(c, a.cardTitle(id), func() {
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
							mode := a.snapshot.CurrentMode
							if ui.Select(c.Key("mode-select"), &mode, a.snapshot.Modes).FillWidth().Label(a.t("代理模式", "Proxy mode")).Changed() {
								next := mode
								a.rpc("mode", func(ctx context.Context, client *api.Client) error { return client.SetClashMode(ctx, next) })
							}
						}
					})
				}
			})
			a.unsupported(c, "status")
			a.unsupported(c, "clashMode")
		})
	})
}
func (a *App) cardManager(c *ui.Context) {
	ui.DialogBase(c, &a.cardDialog, func(back, panel ui.Element) {
		panel.Width(480).MaxWidthPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(14)
		ui.Text(c, a.t("仪表盘卡片", "Dashboard Items")).FontSize(20).Bold()
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
				a.iconButton(c, "up-"+id, a.t("上移 ", "Move up ")+a.cardTitle(id), "↑", func() {
					if i > 0 {
						next := append([]string(nil), order...)
						next[i-1], next[i] = next[i], next[i-1]
						a.settings.Cards = next
						a.saveSettings()
					}
				})
				a.iconButton(c, "down-"+id, a.t("下移 ", "Move down ")+a.cardTitle(id), "↓", func() {
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
			ui.Button(c, a.t("恢复默认", "Reset")).OnClick(func() {
				a.settings.Cards = append([]string(nil), cardIDs...)
				a.settings.DisabledCards = nil
				a.saveSettings()
			})
			ui.Spacer(c)
			ui.PrimaryButton(c, a.t("完成", "Done")).OnClick(func() { a.cardDialog = false })
		})
	})
}
