package app

import (
	"context"
	"github.com/xmdhs/boxpilot/locales"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/model"
)

func (a *App) visibleLogs() []model.LogEntry {
	entries := a.snapshot.Logs
	if a.logsPaused {
		entries = a.frozenLogs
	}
	result := make([]model.LogEntry, 0, len(entries))
	query := strings.ToLower(a.logsQuery)
	for _, e := range entries {
		if a.logLevel != "all" && e.Level.String() != a.logLevel {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(plainLog(e.Message)), query) {
			continue
		}
		result = append(result, e)
	}
	return result
}
func (a *App) logs(c *ui.Context) {
	a.pageTitle(c, locales.In(a.language()).Logs(), func() {
		pause := locales.In(a.language()).PauseUpdates()
		if a.logsPaused {
			pause = locales.In(a.language()).ResumeUpdates()
		}
		transitionedButton(c, pause).OnClick(func() {
			a.logsPaused = !a.logsPaused
			if a.logsPaused {
				a.frozenLogs = append([]model.LogEntry(nil), a.snapshot.Logs...)
			} else {
				a.frozenLogs = nil
				a.logList.ScrollToEnd()
			}
		})
		if transitionedButton(c, locales.In(a.language()).CopyLogs()).Clicked() {
			var b strings.Builder
			for _, e := range a.visibleLogs() {
				b.WriteString(a.redactSensitive(plainLog(e.Message)))
				b.WriteByte('\n')
			}
			c.WriteClipboardThen(b.String(), func(err error) {
				if err != nil {
					a.fail(err)
					return
				}
			})
		}
		transitionedButton(c, locales.In(a.language()).ClearLogs()).Disabled(a.client == nil).OnClick(func() {
			a.confirm(locales.In(a.language()).ClearLogs(), "", func() {
				a.rpc("clear-logs", func(ctx context.Context, client *api.Client) error { return client.ClearLogs(ctx) })
			})
		})
	})
	if a.logsPaused {
		a.label(c, locales.In(a.language()).UpdatesArePausedThisIsAFrozenSnapshotResumeToSeeTheLatestLogs())
	}
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.SearchField(c.Key("log-query"), &a.logsQuery).Grow(1).MinWidth(0).Label(locales.In(a.language()).Search())
		all := locales.In(a.language()).All()
		level := a.logLevel
		if level == "all" {
			level = all
		}
		if ui.Select(c.Key("log-level"), &level, []string{all, "PANIC", "FATAL", "ERROR", "WARN", "INFO", "DEBUG", "TRACE"}).Width(110).Shrink(0).Label(locales.In(a.language()).LogLevel()).Changed() {
			a.logLevel = level
			if level == all {
				a.logLevel = "all"
			}
		}
	})
	a.unsupported(c, "logs")
	entries := a.visibleLogs()
	a.logList.Key = func(i int) any { return entries[i].ID }
	ui.List(c.Key("log-list"), &a.logList, len(entries), func(i int) {
		entry := entries[i]
		spans := a.logSpans(a.redactSensitive(entry.Message), a.logsQuery)
		ui.Row(c).FillWidth().Gap(12).Padding(5, 12).AlignItems(ui.Start).Children(func() {
			color := a.palette.muted
			if entry.Level <= 2 {
				color = a.palette.danger
			} else if entry.Level == 3 {
				color = a.palette.medium
			}
			ui.Text(c, entry.Level.String()).Font("monospace").FontSize(10).Width(48).TextColor(color)
			ui.RichText(c, spans...).Font("monospace").FontSize(12).Grow(1).Selectable()
		})
	}).Grow(1).Background(a.palette.card).Border(1, a.palette.border).Radius(12)
	if len(entries) == 0 {
		a.label(c, locales.In(a.language()).EmptyLogs())
	}
}

type ansiRun struct {
	text  string
	color ui.Color
	bold  bool
}

// 只解释 SGR 样式；丢弃 OSC/其他终端控制，不把服务端文本变成可执行链接。
func parseLog(text string, defaultColor ui.Color) []ansiRun {
	color := defaultColor
	bold := false
	var runs []ansiRun
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			runs = append(runs, ansiRun{b.String(), color, bold})
			b.Reset()
		}
	}
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			flush()
			i++
			if i >= len(text) {
				break
			}
			if text[i] == '[' {
				i++
				start := i
				for i < len(text) && (text[i] < 0x40 || text[i] > 0x7e) {
					i++
				}
				if i >= len(text) {
					break
				}
				params := text[start:i]
				command := text[i]
				i++
				if command == 'm' {
					if params == "" {
						params = "0"
					}
					parts := strings.Split(params, ";")
					for j := 0; j < len(parts); j++ {
						v, _ := strconv.Atoi(parts[j])
						switch {
						case v == 0:
							color = defaultColor
							bold = false
						case v == 1:
							bold = true
						case v == 22:
							bold = false
						case v == 39:
							color = defaultColor
						case v >= 30 && v <= 37:
							color = ansiColor(v - 30)
						case v >= 90 && v <= 97:
							color = ansiColor(v - 90 + 8)
						case v == 38 && j+4 < len(parts) && parts[j+1] == "2":
							r, _ := strconv.Atoi(parts[j+2])
							g, _ := strconv.Atoi(parts[j+3])
							bl, _ := strconv.Atoi(parts[j+4])
							color = ui.RGB(uint8(max(0, min(255, r))), uint8(max(0, min(255, g))), uint8(max(0, min(255, bl))))
							j += 4
						case v == 38 && j+2 < len(parts) && parts[j+1] == "5":
							n, _ := strconv.Atoi(parts[j+2])
							color = ansiColor(n)
							j += 2
						}
					}
				}
				continue
			}
			if text[i] == ']' {
				i++
				for i < len(text) {
					if text[i] == 7 {
						i++
						break
					}
					if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
				continue
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		if r == '\t' || r == '\n' || r >= 32 {
			b.WriteRune(r)
		}
	}
	flush()
	return runs
}
func ansiColor(n int) ui.Color {
	base := []string{"#666666", "#c25e64", "#5b9b73", "#b39848", "#5c89c5", "#a17bb7", "#4f9da1", "#bbbbbb", "#888888", "#e07b80", "#84bc96", "#d0bb78", "#91b3e0", "#c0a1d2", "#82c0c4", "#eeeeee"}
	if n < 0 {
		n = 0
	}
	if n < 16 {
		return ui.Hex(base[n])
	}
	if n >= 232 {
		v := uint8(min(255, 8+(n-232)*10))
		return ui.RGB(v, v, v)
	}
	n -= 16
	v := func(c int) uint8 {
		if c == 0 {
			return 0
		}
		return uint8(55 + 40*c)
	}
	return ui.RGB(v(n/36), v(n/6%6), v(n%6))
}
func plainLog(text string) string {
	var b strings.Builder
	for _, r := range parseLog(text, ui.Color{}) {
		b.WriteString(r.text)
	}
	return b.String()
}
func (a *App) logSpans(text, query string) []ui.Span {
	var spans []ui.Span
	needle := []rune(query)
	for _, run := range parseLog(text, a.palette.text) {
		runes := []rune(run.text)
		weight := 400
		if run.bold {
			weight = 600
		}
		add := func(s string, highlight bool) {
			if s == "" {
				return
			}
			span := ui.Span{Text: s, Color: run.color, Weight: weight}
			if highlight {
				span.Background = a.palette.accent.Alpha(.22)
				span.Radius = 2
			}
			spans = append(spans, span)
		}
		if len(needle) == 0 {
			add(run.text, false)
			continue
		}
		start := 0
		for i := 0; i+len(needle) <= len(runes); {
			if strings.EqualFold(string(runes[i:i+len(needle)]), query) {
				add(string(runes[start:i]), false)
				add(string(runes[i:i+len(needle)]), true)
				i += len(needle)
				start = i
			} else {
				i++
			}
		}
		add(string(runes[start:]), false)
	}
	return spans
}
