package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"boxpilot/internal/api"
	"boxpilot/internal/model"
	"github.com/egoist/mygo/ui"
)

func (a *App) connections(c *ui.Context) {
	a.pageTitle(c, "连接", "Connections", func() {
		ui.Button(c, a.t("关闭全部", "Close All")).Disabled(a.client == nil).OnClick(func() {
			a.confirm(a.t("关闭全部连接？", "Close all connections?"), a.t("当前实例上的活动连接都会被关闭。", "All active connections on the selected instance will be closed."), func() {
				a.rpc("close-all", func(ctx context.Context, client *api.Client) error { return client.CloseAllConnections(ctx) })
			})
		})
	})
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.SearchField(c.Key("connection-query"), &a.connectionQuery).Grow(1).Label(a.t("搜索连接", "Search connections"))
		filter := a.settings.ConnectionFilter
		if filter == "" {
			filter = "active"
		}
		if ui.Select(c.Key("connection-filter"), &filter, []string{"active", "closed", "all"}).Width(100).Label(a.t("连接状态", "Connection state")).Changed() {
			a.settings.ConnectionFilter = filter
			a.saveSettings()
		}
		sort := a.settings.ConnectionSort
		if sort == "" {
			sort = "date"
		}
		if ui.Select(c.Key("connection-sort"), &sort, []string{"date", "traffic", "trafficTotal"}).Width(125).Label(a.t("连接排序", "Sort connections")).Changed() {
			a.settings.ConnectionSort = sort
			a.saveSettings()
		}
	})
	a.unsupported(c, "connections")
	rows := model.FilterConnections(a.snapshot.Connections, a.connectionQuery, a.settings.ConnectionFilter, a.settings.ConnectionSort)
	a.label(c, fmt.Sprintf(a.t("%d 条连接 · 支持 network: / inbound: / outbound: 等筛选", "%d connections · network: / inbound: / outbound: filters supported"), len(rows)))
	a.connectionList.Key = func(i int) any { return rows[i].Connection.GetId() }
	ui.List(c.Key("connections-list"), &a.connectionList, len(rows), func(i int) {
		row := rows[i]
		conn := row.Connection
		b := ui.ButtonBase(c.Key(conn.GetId())).FillWidth().Padding(15).Radius(12).Background(a.palette.card).Border(1, a.palette.border).Label(conn.GetId())
		if b.Hovered() {
			b.Background(a.palette.hover)
		}
		b.Children(func() {
			ui.Column(c).Gap(9).FillWidth().Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					a.pill(c, strings.ToUpper(conn.GetNetwork()), a.palette.muted)
					dest := conn.GetDomain()
					if dest == "" {
						dest = conn.GetDestination()
					}
					ui.Text(c, dest).Grow(1).SingleLine().Font("monospace").FontSize(12)
					if row.ClosedAt == 0 {
						a.pill(c, a.t("活动", "Active"), a.palette.good)
					} else {
						a.pill(c, a.t("已关闭", "Closed"), a.palette.danger)
					}
				})
				ui.Row(c).Gap(20).Children(func() {
					ui.Text(c, "↑ "+model.FormatBytes(row.UplinkRate)+"/s   ↓ "+model.FormatBytes(row.DownlinkRate)+"/s").Font("monospace").FontSize(11).Grow(1)
					a.label(c, conn.GetInboundType()+" / "+conn.GetInbound()).SingleLine()
				})
				ui.Row(c).Gap(16).Children(func() {
					a.label(c, "↑ "+model.FormatBytes(row.UplinkTotal)+"   ↓ "+model.FormatBytes(row.DownlinkTotal)).Font("monospace").Grow(1)
					a.label(c, conn.GetOutbound()).SingleLine()
				})
			})
		})
		b.OnClick(func() { a.detailID = conn.GetId() })
	}).Grow(1).Gap(8)
	if len(rows) == 0 {
		a.label(c, a.t("暂无匹配连接", "No matching connections"))
	}
	if a.detailID != "" {
		for _, row := range a.snapshot.Connections {
			if row.Connection.GetId() == a.detailID {
				a.connectionDetail(c, row)
				break
			}
		}
	}
}
func (a *App) connectionDetail(c *ui.Context, row model.ConnectionRow) {
	width, _ := c.Size()
	panelWidth := float32(420)
	if width < 720 {
		panelWidth = width
	}
	ui.Overlay(c, func() {
		back := ui.Row(c).Absolute().Top(0).Bottom(0).Left(0).Right(0).Justify(ui.End).Background(ui.RGBA(0, 0, 0, .28)).Modal()
		back.Children(func() {
			panel := ui.Column(c).Width(panelWidth).FillHeight().Padding(24).Gap(16).Background(a.palette.bg).Role(ui.RoleDialog).Label(a.t("连接详情", "Connection details"))
			panel.Children(func() {
				conn := row.Connection
				ui.Row(c).FillWidth().Children(func() {
					ui.Text(c, a.t("连接详情", "Connection")).FontSize(22).Bold().Grow(1)
					a.iconButton(c, "close-detail", a.t("关闭详情", "Close details"), "×", func() { a.detailID = "" })
				})
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Gap(12).Children(func() {
						a.label(c, conn.GetId()).Selectable()
						a.card(c, a.t("流量", "Traffic"), func() {
							a.line(c, a.t("创建时间", "Created"), time.UnixMilli(conn.GetCreatedAt()).Format("2006-01-02 15:04:05"))
							a.line(c, a.t("上传", "Uplink"), model.FormatBytes(row.UplinkTotal))
							a.line(c, a.t("下载", "Downlink"), model.FormatBytes(row.DownlinkTotal))
							if row.ClosedAt > 0 {
								a.line(c, a.t("关闭时间", "Closed"), time.UnixMilli(row.ClosedAt).Format("2006-01-02 15:04:05"))
							}
						})
						a.card(c, a.t("元数据", "Metadata"), func() {
							a.line(c, a.t("入站", "Inbound"), conn.GetInbound())
							a.line(c, a.t("入站类型", "Inbound type"), conn.GetInboundType())
							a.line(c, a.t("网络", "Network"), conn.GetNetwork())
							a.line(c, a.t("源地址", "Source"), conn.GetSource())
							a.line(c, a.t("目标地址", "Destination"), conn.GetDestination())
							a.line(c, a.t("域名", "Domain"), conn.GetDomain())
							a.line(c, a.t("协议", "Protocol"), conn.GetProtocol())
							a.line(c, a.t("规则", "Rule"), conn.GetRule())
							a.line(c, a.t("出站", "Outbound"), conn.GetOutbound())
							a.line(c, a.t("出站链路", "Chain"), strings.Join(conn.GetChainList(), " → "))
						})
						if process := conn.GetProcessInfo(); process != nil {
							a.card(c, a.t("进程", "Process"), func() {
								a.line(c, "PID", fmt.Sprint(process.GetProcessId()))
								a.line(c, a.t("路径", "Path"), process.GetProcessPath())
								a.line(c, a.t("用户", "User"), process.GetUserName())
							})
						}
						if row.ClosedAt == 0 {
							ui.Button(c, a.t("关闭此连接", "Close Connection")).TextColor(a.palette.danger).OnClick(func() {
								id := conn.GetId()
								a.rpc("close-"+id, func(ctx context.Context, client *api.Client) error { return client.CloseConnection(ctx, id) })
								a.detailID = ""
							})
						}
					})
				})
			})
			if panel.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) {
				a.detailID = ""
			}
		})
	})
}
