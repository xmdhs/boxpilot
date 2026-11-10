package app

import (
	"context"
	"fmt"

	"boxpilot/internal/api"
	"github.com/egoist/mygo/ui"
)

func (a *App) groups(c *ui.Context) {
	a.pageTitle(c, "分组", "Groups", nil)
	a.unsupported(c, "groups")
	if len(a.snapshot.Groups) == 0 {
		a.empty(c, "暂无分组，请连接已启动的实例", "No groups. Connect to a running instance.")
		return
	}
	width, _ := c.Size()
	room := width - 232 - 90
	if width <= 720 {
		room = width - 80
	}
	columns := max(1, int(room/180))
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(16).FillWidth().Children(func() {
			for _, g := range a.snapshot.Groups {
				g := g
				ui.Column(c.Key(g.GetTag())).FillWidth().Padding(20).Gap(16).Radius(16).Background(a.palette.card).Border(1, a.palette.border).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, g.GetTag()).Bold()
						a.label(c, g.GetType()).Grow(1)
						a.pill(c, fmt.Sprint(len(g.GetItems())), a.palette.muted)
						ui.Button(c, a.t("测速", "URL test")).Disabled(a.busy["test-"+g.GetTag()]).OnClick(func() {
							a.rpc("test-"+g.GetTag(), func(ctx context.Context, client *api.Client) error { return client.URLTest(ctx, g.GetTag()) })
						})
						expandLabel := a.t("展开", "Expand")
						if g.GetIsExpand() {
							expandLabel = a.t("收起", "Collapse")
						}
						ui.Button(c, expandLabel).OnClick(func() {
							next := !g.GetIsExpand()
							a.rpc("expand-"+g.GetTag(), func(ctx context.Context, client *api.Client) error {
								return client.SetGroupExpand(ctx, g.GetTag(), next)
							})
						})
					})
					if g.GetIsExpand() {
						ui.Grid(c).Columns(columns).Gap(9).FillWidth().Children(func() {
							for _, item := range g.GetItems() {
								item := item
								b := ui.ButtonBase(c.Key(item.GetTag())).Padding(12).Radius(10).Border(1, a.palette.border).MinHeight(68).Label(item.GetTag())
								if item.GetTag() == g.GetSelected() {
									b.Background(a.palette.soft).Border(1, a.palette.accent)
								} else if b.Hovered() {
									b.Background(a.palette.hover)
								}
								b.Children(func() {
									ui.Column(c).FillWidth().Gap(6).Children(func() {
										ui.Text(c, item.GetTag()).SingleLine()
										ui.Row(c).Gap(6).Children(func() {
											a.label(c, item.GetType()).FontSize(11).Grow(1)
											if item.GetUrlTestDelay() > 0 {
												ui.Text(c, fmt.Sprintf("%d ms", item.GetUrlTestDelay())).Font("monospace").FontSize(11).TextColor(a.delayColor(item.GetUrlTestDelay()))
											}
										})
									})
								})
								b.Disabled(!g.GetSelectable() || a.busy["select-"+g.GetTag()]).OnClick(func() {
									if item.GetTag() != g.GetSelected() {
										a.rpc("select-"+g.GetTag(), func(ctx context.Context, client *api.Client) error {
											return client.SelectOutbound(ctx, g.GetTag(), item.GetTag())
										})
									}
								})
								b.ContextMenu(func(m *ui.Menu) {
									if m.Item(a.t("测试此节点", "Test this node")).Chosen() {
										a.rpc("node-test-"+item.GetTag(), func(ctx context.Context, client *api.Client) error { return client.URLTest(ctx, item.GetTag()) })
									}
								})
							}
						})
					} else {
						ui.Row(c).Gap(5).Wrap().Children(func() {
							for _, item := range g.GetItems() {
								color := a.delayColor(item.GetUrlTestDelay())
								selected := item.GetTag() == g.GetSelected()
								ui.Box(c).Size(11, 11).Radius(3).Background(color).Tooltip(fmt.Sprintf("%s · %d ms", item.GetTag(), item.GetUrlTestDelay())).DrawOver(func(p *ui.Painter, r ui.Rect) {
									if selected {
										p.Fill(ui.Rect{X: r.X + 4, Y: r.Y + 4, W: 3, H: 3}, ui.Hex("#ffffff"), 2)
									}
								})
							}
						})
					}
				})
			}
		})
	})
}
func (a *App) delayColor(delay int32) ui.Color {
	if delay <= 0 {
		return a.palette.faint
	}
	if delay < 800 {
		return a.palette.good
	}
	if delay < 1500 {
		return a.palette.medium
	}
	return a.palette.bad
}
