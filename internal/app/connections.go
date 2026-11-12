package app

import (
	"context"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/model"
)

func (a *App) connections(c *ui.Context) {
	a.pageTitle(c, locales.In(a.language()).Connections(), func() {
		ui.Button(c, locales.In(a.language()).CloseAllConnections()).Disabled(a.client == nil).OnClick(func() {
			a.confirm(locales.In(a.language()).CloseAllConnections(), "", func() {
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
		ui.SearchField(c.Key("connection-query"), &a.connectionQuery).FillWidth().MinWidth(0).Label(locales.In(a.language()).Search())
	}
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		if !narrow {
			ui.SearchField(c.Key("connection-query"), &a.connectionQuery).Grow(1).MinWidth(0).Label(locales.In(a.language()).Search())
		}
		filterValues := []string{"active", "closed", "all"}
		filterOptions := []string{locales.In(a.language()).Active(), locales.In(a.language()).Closed(), locales.In(a.language()).All()}
		filterOption := filterOptions[0]
		for i, value := range filterValues {
			if filter == value {
				filterOption = filterOptions[i]
			}
		}
		if ui.Select(c.Key("connection-filter"), &filterOption, filterOptions).Width(100).Shrink(0).Label(locales.In(a.language()).State()).Changed() {
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
		sortOptions := []string{locales.In(a.language()).ByDate(), locales.In(a.language()).ByTraffic(), locales.In(a.language()).ByTrafficTotal()}
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
		if ui.Select(c.Key("connection-sort"), &sortOption, sortOptions).Width(125).Shrink(0).Label(locales.In(a.language()).SortBy()).Changed() {
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
						a.pill(c, locales.In(a.language()).Active(), a.palette.good)
					} else {
						a.pill(c, locales.In(a.language()).Closed(), a.palette.danger)
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
		empty := locales.In(a.language()).NoConnections()
		switch filter {
		case "active":
			empty = locales.In(a.language()).NoActiveConnections()
		case "closed":
			empty = locales.In(a.language()).NoClosedConnections()
		}
		if strings.TrimSpace(a.connectionQuery) != "" {
			empty = locales.In(a.language()).NoMatchingConnections()
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
			panel := ui.Column(c).Width(panelWidth).FillHeight().Padding(24).Gap(16).Background(a.palette.bg).Role(ui.RoleDialog).Label(locales.In(a.language()).Connection()).Transition(drawerTransition(24))
			panel.Children(func() {
				conn := row.Connection
				ui.Row(c).FillWidth().Children(func() {
					ui.Text(c, locales.In(a.language()).Connection()).FontSize(22).Bold().Grow(1)
					a.iconButton(c, "close-detail", locales.In(a.language()).Close(), "×", func() { a.detailID = "" })
				})
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Gap(12).Children(func() {
						a.label(c, conn.GetId()).Selectable()
						a.card(c, locales.In(a.language()).Traffic(), func() {
							a.line(c, locales.In(a.language()).CreatedAt(), time.UnixMilli(conn.GetCreatedAt()).Format("2006-01-02 15:04:05"))
							a.line(c, locales.In(a.language()).Uplink(), model.FormatBytes(row.UplinkTotal))
							a.line(c, locales.In(a.language()).Downlink(), model.FormatBytes(row.DownlinkTotal))
							if row.ClosedAt > 0 {
								a.line(c, locales.In(a.language()).Closed(), time.UnixMilli(row.ClosedAt).Format("2006-01-02 15:04:05"))
							}
						})
						a.card(c, locales.In(a.language()).Metadata(), func() {
							a.line(c, locales.In(a.language()).Inbound(), conn.GetInbound())
							a.line(c, locales.In(a.language()).InboundType(), conn.GetInboundType())
							a.line(c, locales.In(a.language()).Network(), conn.GetNetwork())
							a.line(c, locales.In(a.language()).Source(), conn.GetSource())
							a.line(c, locales.In(a.language()).Destination(), conn.GetDestination())
							a.line(c, locales.In(a.language()).Domain(), conn.GetDomain())
							a.line(c, locales.In(a.language()).Protocol(), conn.GetProtocol())
							a.line(c, locales.In(a.language()).MatchRule(), conn.GetRule())
							a.line(c, locales.In(a.language()).Outbound(), conn.GetOutbound())
							a.line(c, locales.In(a.language()).Chain(), strings.Join(conn.GetChainList(), " → "))
						})
						if process := conn.GetProcessInfo(); process != nil {
							a.card(c, locales.In(a.language()).Process(), func() {
								a.line(c, "PID", fmt.Sprint(process.GetProcessId()))
								a.line(c, locales.In(a.language()).User(), process.GetUserName())
							})
						}
						if row.ClosedAt == 0 {
							ui.Button(c, locales.In(a.language()).CloseConnection()).TextColor(a.palette.danger).OnClick(func() {
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
