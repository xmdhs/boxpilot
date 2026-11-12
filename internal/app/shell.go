package app

import (
	"fmt"
	"github.com/xmdhs/boxpilot/locales"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var pages = []struct{ id, key, icon string }{{"overview", "overview", "dashboard"}, {"groups", "groups", "folder"}, {"connections", "connections", "swap_vert"}, {"logs", "logs", "text_snippet"}, {"tools", "tools", "terminal"}, {"settings", "settings", "settings"}, {"core", "core", "memory"}}

func pageLabel(locale, key string) string {
	m := locales.In(locale)
	switch key {
	case "overview":
		return m.Overview()
	case "groups":
		return m.Groups()
	case "connections":
		return m.Connections()
	case "logs":
		return m.Logs()
	case "tools":
		return m.Tools()
	case "settings":
		return m.Settings()
	case "core":
		return m.Core()
	default:
		return key
	}
}

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
			a.sidebar(c, false)
		}
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			a.toolbar(c, compact)
			ui.Column(c.Key(a.page)).FillWidth().Grow(1).MinHeight(0).Padding(pagePadding).Gap(pageGap).Transition(pageTransition).Children(func() {
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
			back := ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Background(ui.RGBA(0, 0, 0, .3)).Modal().Transition(overlayTransition)
			back.Children(func() {
				panel := ui.Column(c).Width(sidebarWidth).FillHeight().Transition(navigationDrawerTransition()).Children(func() { a.sidebar(c, true) })
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
				ui.Text(c, locales.In(a.language()).ServiceStopping())
			})
		})
	}
}

func (a *App) sidebar(c *ui.Context, showServerPicker bool) {
	p := a.palette
	ui.Column(c).Width(sidebarWidth).Shrink(0).FillHeight().Background(p.surface).Padding(16, 12).Gap(6).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
		ui.Column(c).Padding(12, 14, 26).Gap(2).Children(func() {
			ui.Text(c, "BoxPilot").Font("Source Serif 4, Georgia, serif").FontSize(23).FontWeight(560)
		})
		for _, page := range pages {
			page := page
			b := ui.ButtonBase(c.Key(page.id)).FillWidth().Height(38).Radius(10).PaddingX(14).Label(pageLabel(a.language(), page.key)).Transition(interactionTransition)
			color := p.muted
			if a.page == page.id {
				b.Background(p.soft)
				color = p.accent
			} else if b.Hovered() {
				b.Background(p.hover)
			}
			b.TextColor(color).Children(func() {
				ui.Icon(c, icons[page.icon]).Size(17, 17)
				ui.Text(c, pageLabel(a.language(), page.key)).FontSize(13).FontWeight(500).Grow(1)
			})
			b.Gap(12).OnClick(func() { a.page = page.id; a.compactMenu = false })
		}
		ui.Spacer(c)
		ui.Column(c).Padding(12).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
			if showServerPicker {
				a.serverPicker(c, true)
			}
			ui.Row(c).Gap(6).Children(func() {
				a.pill(c, a.phaseLabel(), a.statusColor(a.snapshot.Phase))
				if a.snapshot.Version != "" {
					a.label(c, a.snapshot.Version).FontSize(10)
				}
			})
			transitionedButton(c, locales.In(a.language()).Quit()).FillWidth().OnClick(mygo.App.Quit)
		})
	})
}

func (a *App) serverPicker(c *ui.Context, compact bool) {
	choices := []string{locales.In(a.language()).ThisComputer()}
	ids := []string{localID}
	for _, server := range a.settings.Servers {
		choices = append(choices, server.Name+" · "+server.ID[:min(6, len(server.ID))])
		ids = append(ids, server.ID)
	}
	choice := choices[0]
	for i, id := range ids {
		if id == a.settings.ActiveID {
			choice = choices[i]
		}
	}
	pickerWidth := float32(200)
	if compact {
		pickerWidth = 150
	}
	if ui.Select(c.Key("active-server"), &choice, choices).Width(pickerWidth).Label(locales.In(a.language()).Servers()).Changed() {
		for i, label := range choices {
			if label == choice {
				a.activate(ids[i])
				break
			}
		}
	}
}

func (a *App) toolbar(c *ui.Context, compact bool) {
	ui.Row(c).FillWidth().Height(52).PaddingX(20).Gap(12).AlignItems(ui.Center).Background(a.palette.bg).BorderWidth(0, 0, 1, 0).BorderColor(a.palette.border).Children(func() {
		if compact {
			a.iconButton(c, "navigation", locales.In(a.language()).ToggleNavigation(), "☰", func() { a.compactMenu = !a.compactMenu })
		}
		ui.Text(c, pageLabel(a.language(), a.page)).Font("Source Serif 4, Georgia, serif").FontSize(20).FontWeight(560).TextColor(a.palette.text)
		ui.Spacer(c)
		if !compact {
			a.serverPicker(c, false)
		}
		if a.settings.ActiveID == localID || a.settings.ActiveID == "" {
			label := locales.In(a.language()).Start()
			if a.coreState.Phase == "running" {
				label = locales.In(a.language()).Stop()
			}
			transitionedButton(c, label).Disabled(a.busy["core-start"] || a.busy["core-stop"] || a.closing).OnClick(a.toggleCore)
		} else {
			a.iconButton(c, "reconnect", locales.In(a.language()).Retry(), "↻", func() { a.activate(a.settings.ActiveID) })
		}
		if a.page == "overview" {
			a.iconButton(c, "manage-cards", locales.In(a.language()).DashboardItems(), "⚙", func() { a.cardDialog = true })
		}
	})
}
func (a *App) activeName() string {
	if a.settings.ActiveID == localID || a.settings.ActiveID == "" {
		return locales.In(a.language()).ThisComputer()
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
		return locales.In(a.language()).Connected()
	case "connecting":
		return locales.In(a.language()).Connecting()
	case "error":
		return locales.In(a.language()).ConnectionFailed()
	}
	return locales.In(a.language()).Unavailable()
}
func (a *App) streamBanner(c *ui.Context) {
	if a.snapshot.Error != "" {
		ui.Text(c, a.redactSensitive(a.snapshot.Error)).FontSize(12).TextColor(a.palette.danger).Background(a.palette.danger.Alpha(.08)).Padding(12).Radius(10).FillWidth().Selectable()
	}
	if a.snapshot.Phase == "connecting" {
		ui.Row(c).Gap(10).Children(func() {
			ui.Spinner(c).Size(15, 15)
			a.label(c, locales.In(a.language()).Connecting())
		})
	}
}
func (a *App) pageTitle(c *ui.Context, _ string, actions func()) {
	if actions != nil {
		ui.Row(c).FillWidth().Gap(8).Wrap().Justify(ui.End).Children(actions)
	}
	a.streamBanner(c)
}
func (a *App) dialogs(c *ui.Context) {
	services := c.Services()
	a.serverForm(c)
	a.profileForm(c)
	a.cardManager(c)
	ui.DialogBase(c.Key("error-dialog"), &a.errorOpen, func(back, panel ui.Element) {
		animateDialog(back, panel)
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(480).MaxWidthPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(16)
		ui.Text(c, locales.In(a.language()).Error()).FontSize(20).Bold()
		ui.Scroll(c).MaxHeight(300).Children(func() { ui.Text(c, a.errorText).Selectable().Font("monospace").FontSize(12) })
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			transitionedButton(c, locales.In(a.language()).Copy()).OnClick(func() { services.WriteClipboard(a.errorText) })
			transitionedPrimaryButton(c, locales.In(a.language()).Ok()).OnClick(func() { a.errorOpen = false })
		})
	})
	ui.DialogBase(c.Key("confirm-dialog"), &a.confirmOpen, func(back, panel ui.Element) {
		animateDialog(back, panel)
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(480).MaxWidthPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(16)
		ui.Text(c, a.confirmTitle).FontSize(20).Bold()
		ui.Text(c, a.confirmMessage)
		ui.Row(c).Justify(ui.End).Gap(10).Children(func() {
			transitionedButton(c, locales.In(a.language()).Cancel()).OnClick(func() { a.confirmOpen = false })
			transitionedPrimaryButton(c, a.confirmButtonLabel()).OnClick(func() {
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
