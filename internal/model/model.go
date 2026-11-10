package model

import (
	"cmp"
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"time"

	"boxpilot/internal/gen/daemon"
)

type ConnectionRow struct {
	Connection                                           *daemon.Connection
	UplinkRate, DownlinkRate, UplinkTotal, DownlinkTotal uint64
	ClosedAt                                             int64 // Unix milliseconds; zero means active.
}

type LogEntry struct {
	ID      uint64
	Level   daemon.LogLevel
	Message string
}

// FilterConnections returns a new slice, never rearranging rows. Search tokens
// are ANDed and use the dashboard's field syntax and case-insensitive matching.
func FilterConnections(rows []ConnectionRow, query, filter, sortMode string) []ConnectionRow {
	result := make([]ConnectionRow, 0, len(rows))
	tokens := strings.Fields(strings.ToLower(query))
	for _, row := range rows {
		if row.Connection == nil || (filter == "active" && row.ClosedAt != 0) || (filter == "closed" && row.ClosedAt == 0) {
			continue
		}
		if matches(row.Connection, tokens) {
			result = append(result, row)
		}
	}
	slices.SortStableFunc(result, func(a, b ConnectionRow) int {
		var order int
		switch sortMode {
		case "traffic":
			order = compareSum(b.UplinkRate, b.DownlinkRate, a.UplinkRate, a.DownlinkRate)
		case "trafficTotal":
			order = compareSum(b.UplinkTotal, b.DownlinkTotal, a.UplinkTotal, a.DownlinkTotal)
		}
		if order != 0 {
			return order
		}
		return cmp.Compare(b.Connection.CreatedAt, a.Connection.CreatedAt)
	})
	return result
}

func compareSum(a, b, c, d uint64) int {
	lo1, hi1 := bits.Add64(a, b, 0)
	lo2, hi2 := bits.Add64(c, d, 0)
	if n := cmp.Compare(hi1, hi2); n != 0 {
		return n
	}
	return cmp.Compare(lo1, lo2)
}

func matches(c *daemon.Connection, tokens []string) bool {
	contains := func(s, q string) bool { return strings.Contains(strings.ToLower(s), q) }
	for _, token := range tokens {
		key, value, field := strings.Cut(token, ":")
		matched := false
		if field {
			switch key {
			case "network":
				matched = strings.EqualFold(c.Network, value)
			case "inbound":
				matched = contains(c.Inbound, value)
			case "inbound.type":
				matched = contains(c.InboundType, value)
			case "source":
				matched = contains(c.Source, value)
			case "destination":
				matched = contains(c.Destination, value)
			case "outbound":
				matched = contains(c.Outbound, value)
			default:
				field = false
			}
		}
		if !field {
			matched = contains(c.Destination, token) || contains(c.Domain, token) || contains(c.Source, token) || contains(c.Outbound, token)
		}
		if !matched {
			return false
		}
	}
	return true
}

func FormatBytes(value uint64) string  { return formatUnits(value, 1000) }
func FormatMemory(value uint64) string { return formatUnits(value, 1024) }
func formatUnits(value, base uint64) string {
	units := [...]string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	v, i := float64(value), 0
	for v >= float64(base) && i < len(units)-1 {
		v /= float64(base)
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", value)
	}
	text := strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
	return text + " " + units[i]
}

// FormatDuration follows the dashboard's compact elapsed-time notation.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d)/float64(time.Millisecond))
	}
	s := d.Seconds()
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", s)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", s)
	}
	minutes := int64(d / time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm %.0fs", minutes, s-float64(minutes*60))
	}
	return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
}
