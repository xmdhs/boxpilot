package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/model"
)

func (a *App) connections(c *ui.Context) {
	a.pageTitle(c, "连接", "Connections", func() {
		ui.Button(c, a.t("关闭所有连接", "Close All Connections")).Disabled(a.client == nil).OnClick(func() {
			a.confirm(a.t("关闭所有连接", "Close All Connections"), "", func() {
				a.rpc("close-all", func(ctx context.Context, client *api.Client) error { return client.CloseAllConnections(ctx) })
			})
		})
	})
	filter := a.settings.ConnectionFilter
	if filter == "" {
		filter = "active"
	}
	narrow := narrowPage(c)
	if narrow {
		ui.SearchField(c.Key("connection-query"), &a.connectionQuery).FillWidth().MinWidth(0).Label(a.t("搜索", "Search"))
	}
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		if !narrow {
			ui.SearchField(c.Key("connection-query"), &a.connectionQuery).Grow(1).MinWidth(0).Label(a.t("搜索", "Search"))
		}
		filterValues := []string{"active", "closed", "all"}
		filterOptions := []string{a.t("活动", "Active"), a.t("已关闭", "Closed"), a.t("全部", "All")}
		filterOption := filterOptions[0]
		for i, value := range filterValues {
			if filter == value {
				filterOption = filterOptions[i]
			}
		}
		if ui.Select(c.Key("connection-filter"), &filterOption, filterOptions).Width(100).Shrink(0).Label(a.t("状态", "State")).Changed() {
			for i, option := range filterOptions {
				if filterOption == option {
					filter = filterValues[i]
					a.settings.ConnectionFilter = filter
					a.saveSettings()
					break
				}
			}
		}
		sortValues := []string{"date", "traffic", "trafficTotal"}
		sortOptions := []string{a.t("按时间", "By date"), a.t("按速率", "By traffic"), a.t("按流量", "By traffic total")}
		sort := a.settings.ConnectionSort
		if sort == "" {
			sort = "date"
		}
		sortOption := sortOptions[0]
		for i, value := range sortValues {
			if sort == value {
				sortOption = sortOptions[i]
			}
		}
		if ui.Select(c.Key("connection-sort"), &sortOption, sortOptions).Width(125).Shrink(0).Label(a.t("排序", "Sort By")).Changed() {
			for i, option := range sortOptions {
				if sortOption == option {
					sort = sortValues[i]
					a.settings.ConnectionSort = sort
					a.saveSettings()
					break
				}
			}
		}
	})
	a.unsupported(c, "connections")
	rows := model.FilterConnections(a.snapshot.Connections, a.connectionQuery, a.settings.ConnectionFilter, a.settings.ConnectionSort)
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
					ui.Row(c).Gap(4).AlignItems(ui.Center).Grow(1).Children(func() {
						ui.Icon(c, icons["upload"]).Size(12, 12).TextColor(a.palette.muted)
						ui.Text(c, model.FormatBytes(row.UplinkRate)+"/s").Font("monospace").FontSize(11)
						ui.Icon(c, icons["download"]).Size(12, 12).TextColor(a.palette.muted)
						ui.Text(c, model.FormatBytes(row.DownlinkRate)+"/s").Font("monospace").FontSize(11)
					})
					a.label(c, conn.GetInboundType()+" / "+conn.GetInbound()).SingleLine()
				})
				ui.Row(c).Gap(16).Children(func() {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Grow(1).Children(func() {
						ui.Icon(c, icons["upload"]).Size(12, 12).TextColor(a.palette.muted)
						a.label(c, model.FormatBytes(row.UplinkTotal)).Font("monospace")
						ui.Icon(c, icons["download"]).Size(12, 12).TextColor(a.palette.muted)
						a.label(c, model.FormatBytes(row.DownlinkTotal)).Font("monospace")
					})
					a.label(c, conn.GetOutbound()).SingleLine()
				})
			})
		})
		b.OnClick(func() { a.detailID = conn.GetId() })
	}).Grow(1).Gap(8)
	if len(rows) == 0 {
		empty := a.t("暂无连接", "No connections")
		switch filter {
		case "active":
			empty = a.t("暂无活动连接", "No active connections")
		case "closed":
			empty = a.t("暂无已关闭连接", "No closed connections")
		}
		if strings.TrimSpace(a.connectionQuery) != "" {
			empty = a.t("没有匹配的连接", "No matching connections")
		}
		a.label(c, empty)
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
	if width <= compactBreakpoint {
		panelWidth = width
	}
	ui.Overlay(c, func() {
		back := ui.Row(c).Absolute().Top(0).Bottom(0).Left(0).Right(0).Justify(ui.End).Background(ui.RGBA(0, 0, 0, .32)).Modal().Transition(overlayTransition)
		back.Children(func() {
			panel := ui.Column(c).Width(panelWidth).FillHeight().Padding(24).Gap(16).Background(a.palette.bg).Role(ui.RoleDialog).Label(a.t("连接", "Connection")).Transition(drawerTransition(20))
			panel.Children(func() {
				conn := row.Connection
				ui.Row(c).FillWidth().Children(func() {
					ui.Text(c, a.t("连接详情", "Connection")).FontSize(22).Bold().Grow(1)
					a.iconButton(c, "close-detail", a.t("关闭", "Close"), "×", func() { a.detailID = "" })
				})
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Gap(12).Children(func() {
						a.label(c, conn.GetId()).Selectable()
						a.card(c, a.t("流量", "Traffic"), func() {
							a.line(c, a.t("创建时间", "Created at"), time.UnixMilli(conn.GetCreatedAt()).Format("2006-01-02 15:04:05"))
							a.line(c, a.t("上行", "Uplink"), model.FormatBytes(row.UplinkTotal))
							a.line(c, a.t("下行", "Downlink"), model.FormatBytes(row.DownlinkTotal))
							if row.ClosedAt > 0 {
								a.line(c, a.t("关闭时间", "Closed"), time.UnixMilli(row.ClosedAt).Format("2006-01-02 15:04:05"))
							}
						})
						a.card(c, a.t("元数据", "Metadata"), func() {
							a.line(c, a.t("入站", "Inbound"), conn.GetInbound())
							a.line(c, a.t("入站类型", "Inbound type"), conn.GetInboundType())
							a.line(c, a.t("网络", "Network"), conn.GetNetwork())
							a.line(c, a.t("源", "Source"), conn.GetSource())
							a.line(c, a.t("目标地址", "Destination"), conn.GetDestination())
							a.line(c, a.t("域名", "Domain"), conn.GetDomain())
							a.line(c, a.t("协议", "Protocol"), conn.GetProtocol())
							a.line(c, a.t("匹配规则", "Match rule"), conn.GetRule())
							a.line(c, a.t("出站", "Outbound"), conn.GetOutbound())
							a.line(c, a.t("链", "Chain"), strings.Join(conn.GetChainList(), " → "))
						})
						if process := conn.GetProcessInfo(); process != nil {
							a.card(c, a.t("进程", "Process"), func() {
								a.line(c, "PID", fmt.Sprint(process.GetProcessId()))
								a.line(c, a.t("用户", "User"), process.GetUserName())
							})
						}
						if row.ClosedAt == 0 {
							ui.Button(c, a.t("关闭连接", "Close connection")).TextColor(a.palette.danger).OnClick(func() {
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
