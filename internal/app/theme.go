package app

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/locales"
)

type palette struct {
	bg, surface, card, text, muted, faint, border, hover, accent, soft, onAccent, good, medium, bad, danger ui.Color
	dark                                                                                                    bool
	theme                                                                                                   *ui.Theme
}

func makePalette(dark bool, accent string) palette {
	p := palette{dark: dark, bg: ui.Hex("#fafafa"), surface: ui.Hex("#f0f0f0"), card: ui.Hex("#ffffff"), text: ui.Hex("#1a1a1a"), muted: ui.Hex("#6e6e6e"), faint: ui.Hex("#9c9c9c"), border: ui.RGBA(0, 0, 0, .09), hover: ui.RGBA(0, 0, 0, .05), good: ui.Hex("#3d8168"), medium: ui.Hex("#a8742f"), bad: ui.Hex("#c25e32"), danger: ui.Hex("#ab2b3f")}
	if dark {
		p.bg = ui.Hex("#1e1e1e")
		p.surface = ui.Hex("#181818")
		p.card = ui.Hex("#292929")
		p.text = ui.Hex("#ededed")
		p.muted = ui.Hex("#a8a8a8")
		p.faint = ui.Hex("#7d7d7d")
		p.border = ui.RGBA(255, 255, 255, .09)
		p.hover = ui.RGBA(255, 255, 255, .06)
		p.good = ui.Hex("#7cb89e")
		p.medium = ui.Hex("#d3a45e")
		p.bad = ui.Hex("#db8a62")
		p.danger = ui.Hex("#e3838f")
	}
	colors := map[string]string{"blue": "#007aff", "purple": "#a550a7", "pink": "#f74f9e", "red": "#ff5257", "orange": "#f7821b", "yellow": "#ffc600", "green": "#62ba46", "graphite": "#8c8c8c"}
	p.accent = p.text
	if color, ok := colors[accent]; ok {
		p.accent = ui.Hex(color)
	} else if len(accent) == 7 && strings.HasPrefix(accent, "#") {
		p.accent = ui.Hex(accent)
	}
	p.soft = p.accent.Alpha(.11)
	p.onAccent = ui.Hex("#ffffff")
	if (float64(p.accent.R)*.2126 + float64(p.accent.G)*.7152 + float64(p.accent.B)*.0722) > 155 {
		p.onAccent = ui.Hex("#1a1a1a")
	}
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	t.Background = p.bg
	t.Surface = p.surface
	t.SurfaceHover = p.hover
	t.SurfacePressed = p.soft
	t.Text = p.text
	t.TextMuted = p.muted
	t.Border = p.border
	t.Accent = p.accent
	t.AccentHover = p.accent
	t.AccentPressed = p.accent
	t.AccentText = p.onAccent
	t.InactiveAccent = p.accent
	t.Focus = p.accent
	t.Danger = p.danger
	t.Success = p.good
	t.Warning = p.medium
	t.FontSize = 13
	t.Radius = 10
	t.Spacing = 4
	p.theme = t
	return p
}

func (a *App) language() string {
	for _, tag := range locales.Catalog.Languages() {
		if a.settings.Language == tag {
			return tag
		}
	}
	return "zh-Hans"
}

// Language returns the supported catalog language selected in persisted settings.
func (a *App) Language() string { return a.language() }

func (a *App) setLanguage(language string) {
	supported := false
	for _, tag := range locales.Catalog.Languages() {
		if tag == language {
			supported = true
			break
		}
	}
	if !supported {
		language = "zh-Hans"
		if a.language() == "en" {
			language = "en"
		}
	}
	a.settings.Language = language
	mygo.App.SetLocale(language)
	if a.desktop != nil {
		a.desktop.SetLanguage(language)
	}
}
func (a *App) label(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).TextColor(a.palette.muted).FontSize(12)
}

func transitionedButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Transition(interactionTransition)
}

func transitionedPrimaryButton(c *ui.Context, label string) ui.Element {
	return ui.PrimaryButton(c, label).Transition(interactionTransition)
}

// transitionedSelect animates the select trigger's colors; MyGo owns the popup.
func transitionedSelect(c *ui.Context, selected *string, options []string) ui.Element {
	return ui.Select(c, selected, options).Transition(interactionTransition)
}

func transitionedSwitch(c *ui.Context, on *bool) ui.Element {
	return ui.Switch(c, on).Transition(interactionTransition)
}

func transitionedCheckbox(c *ui.Context, checked *bool, label string) ui.Element {
	return ui.Checkbox(c, checked, label).Transition(interactionTransition)
}

func transitionedTextInput(c *ui.Context, value *string) ui.Element {
	return ui.TextInput(c, value).Transition(interactionTransition)
}

func transitionedSegmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	return ui.Segmented(c, selected, labels...).Transition(interactionTransition)
}

func (a *App) heading(c *ui.Context, title string) {
	ui.Text(c, title).Font("Source Serif 4, Georgia, serif").FontSize(27).FontWeight(560).TextColor(a.palette.text)
}
func (a *App) card(c *ui.Context, title string, body func()) ui.Element {
	return ui.Column(c).FillWidth().Padding(20).Gap(14).Radius(16).Background(a.palette.card).Border(1, a.palette.border).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, .035)).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(13).Bold()
		}
		body()
	})
}
func (a *App) pill(c *ui.Context, label string, color ui.Color) {
	ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(color).Background(color.Alpha(.12)).Radius(999).Padding(3, 8)
}
func (a *App) line(c *ui.Context, label, value string) {
	if value == "" {
		return
	}
	ui.Row(c).Gap(12).FillWidth().AlignItems(ui.Center).Children(func() {
		a.label(c, label).Grow(1)
		ui.Text(c, value).Font("monospace").FontSize(12).MaxWidth(500).Selectable()
	})
}
func (a *App) field(c *ui.Context, label string, fn func()) {
	ui.Column(c).Gap(6).FillWidth().Children(func() { ui.Text(c, label).FontSize(12).Bold(); fn() })
}
func (a *App) setting(c *ui.Context, title, hint string, fn func()) {
	ui.Row(c).FillWidth().PaddingY(8).Gap(18).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, title)
			if hint != "" {
				a.label(c, hint)
			}
		})
		fn()
	})
}
func (a *App) empty(c *ui.Context, message string) {
	ui.Column(c).Grow(1).FillWidth().Center().Padding(24).Gap(12).Children(func() { a.label(c, message) })
}
func (a *App) statusColor(phase string) ui.Color {
	switch phase {
	case "active", "running":
		return a.palette.good
	case "connecting", "starting", "stopping", "checking":
		return a.palette.medium
	case "error":
		return a.palette.danger
	}
	return a.palette.faint
}
func (a *App) sparkline(c *ui.Context, data []float64) {
	ink := a.palette.accent
	ui.Box(c).Height(58).FillWidth().Draw(func(p *ui.Painter, r ui.Rect) {
		if len(data) < 2 {
			return
		}
		peak := float64(1)
		for _, v := range data {
			peak = math.Max(peak, v)
		}
		peak *= 1.2
		var line, area ui.Path
		area.MoveTo(r.X, r.Y+r.H)
		for i, v := range data {
			x := r.X + r.W*float32(i)/float32(len(data)-1)
			y := r.Y + r.H - 5 - float32(v/peak)*(r.H-10)
			if i == 0 {
				line.MoveTo(x, y)
			} else {
				line.LineTo(x, y)
			}
			area.LineTo(x, y)
		}
		area.LineTo(r.X+r.W, r.Y+r.H).Close()
		p.FillPath(&area, ink.Alpha(.09))
		p.StrokePath(&line, 1.6, ink)
	})
}
func (a *App) iconButton(c *ui.Context, key, label, glyph string, fn func()) {
	a.iconButtonDisabled(c, key, label, glyph, false, fn)
}

func (a *App) iconButtonDisabled(c *ui.Context, key, label, glyph string, disabled bool, fn func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).Size(30, 30).Radius(8).Disabled(disabled).Transition(interactionTransition)
	if b.Hovered() {
		b.Background(a.palette.hover)
	}
	b.Children(func() {
		name := map[string]string{"×": "close", "+": "add", "⚙": "tune", "☰": "menu", "↻": "sync", "↑": "expand_less", "↓": "expand_more", "speed": "speed"}[glyph]
		if name == "" {
			name = glyph
		}
		if icon := icons[name]; icon != nil {
			ui.Icon(c, icon).Size(17, 17)
		} else {
			ui.Text(c, glyph).FontSize(17)
		}
	})
	b.OnClick(fn)
}
func humanRate(n int64) string {
	v := float64(n)
	unit := "bps"
	if v >= 1e9 {
		v /= 1e9
		unit = "Gbps"
	} else if v >= 1e6 {
		v /= 1e6
		unit = "Mbps"
	} else if v >= 1e3 {
		v /= 1e3
		unit = "kbps"
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}
