package app

import (
	"context"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
)

type groupExpandOverride struct {
	expanded   bool
	generation uint64
}

func (a *App) groupExpanded(group *daemon.Group) bool {
	if group == nil {
		return false
	}
	if override, ok := a.groupExpandOverrides[group.GetTag()]; ok && override.generation == a.generation {
		return override.expanded
	}
	return group.GetIsExpand()
}

func (a *App) confirmGroupExpansion(groups []*daemon.Group) {
	for _, group := range groups {
		if group == nil {
			continue
		}
		tag := group.GetTag()
		if override, ok := a.groupExpandOverrides[tag]; ok && (override.generation != a.generation || override.expanded == group.GetIsExpand()) {
			delete(a.groupExpandOverrides, tag)
		}
	}
}

func (a *App) rollbackGroupExpansion(tag string, generation uint64, expanded bool) {
	if override, ok := a.groupExpandOverrides[tag]; ok && override.generation == generation && override.expanded == expanded {
		delete(a.groupExpandOverrides, tag)
	}
}

func (a *App) toggleGroup(group *daemon.Group) {
	if group == nil {
		return
	}
	tag := group.GetTag()
	key := "expand-" + tag
	if a.busy[key] {
		return
	}
	next := !a.groupExpanded(group)
	generation := a.generation
	if a.groupExpandOverrides == nil {
		a.groupExpandOverrides = make(map[string]groupExpandOverride)
	}
	a.groupExpandOverrides[tag] = groupExpandOverride{expanded: next, generation: generation}
	a.rpcWithCallbacks(key, func(ctx context.Context, client *api.Client) error {
		return client.SetGroupExpand(ctx, tag, next)
	}, nil, func(err error) {
		if generation == a.generation {
			if override, ok := a.groupExpandOverrides[tag]; ok && override.generation == generation && override.expanded == next {
				delete(a.groupExpandOverrides, tag)
			}
			a.fail(err)
		}
	})
}

func (a *App) groups(c *ui.Context) {
	a.pageTitle(c, locales.In(a.language()).Groups(), nil)
	a.unsupported(c, "groups")
	if len(a.snapshot.Groups) == 0 {
		a.empty(c, locales.In(a.language()).NoGroups())
		return
	}
	windowWidth, _ := c.Size()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(16).FillWidth().Children(func() {
			for _, g := range a.snapshot.Groups {
				g := g
				ui.Column(c.Key(g.GetTag())).FillWidth().Padding(20).Gap(16).Radius(16).Background(a.palette.card).Border(1, a.palette.border).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, g.GetTag()).Bold()
						a.label(c, g.GetType()).Grow(1)
						a.pill(c, fmt.Sprint(len(g.GetItems())), a.palette.muted)
						a.iconButtonDisabled(c, "test-"+g.GetTag(), locales.In(a.language()).UrlTest(), "speed", a.busy["test-"+g.GetTag()], func() {
							a.rpc("test-"+g.GetTag(), func(ctx context.Context, client *api.Client) error { return client.URLTest(ctx, g.GetTag()) })
						})
						expanded := a.groupExpanded(g)
						expandLabel := locales.In(a.language()).Expand()
						expandIcon := "expand_more"
						if expanded {
							expandLabel = locales.In(a.language()).Collapse()
							expandIcon = "expand_less"
						}
						a.iconButtonDisabled(c, "expand-"+g.GetTag(), expandLabel, expandIcon, a.busy["expand-"+g.GetTag()], func() {
							a.toggleGroup(g)
						})
					})
					if a.groupExpanded(g) {
						ui.Row(c).Wrap().AlignItems(ui.Stretch).Gap(9).FillWidth().Children(func() {
							for _, item := range g.GetItems() {
								item := item
								b := ui.ButtonBase(c.Key(item.GetTag())).Basis(groupItemMinWidth(windowWidth)).Grow(1).MinWidth(0).Padding(12).Radius(10).Border(1, a.palette.border).MinHeight(68).Label(item.GetTag()).Transition(interactionTransition)
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
									if m.Item(locales.In(a.language()).UrlTest()).Chosen() {
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
