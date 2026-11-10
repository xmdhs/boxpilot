package model

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ANSIRun is renderer independent. Color is #rrggbb, or empty for the
// renderer's default foreground. Each SGR replaces (not merges) the style,
// matching sing-box-dashboard and the native Apple client.
type ANSIRun struct {
	Text                    string
	Color                   string
	Bold, Italic, Underline bool
}

var ansiPattern = regexp.MustCompile("\x1b\\[[;\\d]*m")
var ansiColors = map[int]string{30: "#000000", 31: "#ff2159", 32: "#2ecc70", 33: "#e6e600", 34: "#3399db", 35: "#9c59b5", 36: "#5cade3", 37: "#edf0f2"}

func ParseANSI(text string) []ANSIRun {
	var runs []ANSIRun
	style := ANSIRun{}
	cursor := 0
	for _, loc := range ansiPattern.FindAllStringIndex(text, -1) {
		if loc[0] > cursor {
			run := style
			run.Text = text[cursor:loc[0]]
			runs = append(runs, run)
		}
		style = ANSIRun{}
		for _, code := range strings.Split(text[loc[0]+2:loc[1]-1], ";") {
			if code == "" {
				continue
			}
			n, _ := strconv.Atoi(code)
			if n == 0 {
				style = ANSIRun{}
				break
			}
			switch n {
			case 1:
				style.Bold = true
			case 3:
				style.Italic = true
			case 4:
				style.Underline = true
			default:
				if color, ok := ansiColors[n]; ok {
					style.Color = color
				} else if n >= 38 && n <= 125 {
					adjusted := n % 125
					style.Color = fmt.Sprintf("#%02x%02x%02x", adjusted/36*51, adjusted%36/6*51, adjusted%6*51)
				}
			}
		}
		cursor = loc[1]
	}
	if cursor < len(text) {
		style.Text = text[cursor:]
		runs = append(runs, style)
	}
	return runs
}

func StripANSI(text string) string { return ansiPattern.ReplaceAllString(text, "") }

// ANSIColor adjusts a hex foreground against a hex background to WCAG 4.5:1,
// using the same contrast algorithm as the dashboard. Invalid colors are left alone.
func ANSIColor(color, background string) string {
	parse := func(s string) ([3]float64, bool) {
		var out [3]float64
		if len(s) != 7 || s[0] != '#' {
			return out, false
		}
		n, err := strconv.ParseUint(s[1:], 16, 24)
		if err != nil {
			return out, false
		}
		out = [3]float64{float64(n >> 16), float64(n >> 8 & 255), float64(n & 255)}
		return out, true
	}
	c, ok := parse(color)
	if !ok {
		return color
	}
	bg, ok := parse(background)
	if !ok {
		return color
	}
	luminance := func(c [3]float64) float64 {
		for i, v := range c {
			v /= 255
			if v <= .04045 {
				c[i] = v / 12.92
			} else {
				c[i] = math.Pow((v+.055)/1.055, 2.4)
			}
		}
		return .2126*c[0] + .7152*c[1] + .0722*c[2]
	}
	contrast := func(c [3]float64) float64 {
		a, b := luminance(c), luminance(bg)
		return (math.Max(a, b) + .05) / (math.Min(a, b) + .05)
	}
	if contrast(c) >= 4.5 {
		return color
	}
	low, high, best := 0., 1., c
	for range 10 {
		mid := (low + high) / 2
		adjusted := c
		for i, v := range c {
			if luminance(bg) > .5 {
				adjusted[i] = v * (1 - mid)
			} else {
				adjusted[i] = v + (255-v)*mid
			}
		}
		if contrast(adjusted) >= 4.5 {
			best = adjusted
			high = mid
		} else {
			low = mid
		}
	}
	return fmt.Sprintf("#%02x%02x%02x", int(math.Round(best[0])), int(math.Round(best[1])), int(math.Round(best[2])))
}
