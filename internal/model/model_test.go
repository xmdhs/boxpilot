package model

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"boxpilot/internal/gen/daemon"
)

func TestFilterConnections(t *testing.T) {
	rows := []ConnectionRow{
		{Connection: &daemon.Connection{Id: "a", CreatedAt: 1, Network: "tcp", Inbound: "Mixed", InboundType: "mixed", Source: "127.0.0.1:101", Destination: "1.1.1.1:443", Domain: "Example.COM", Outbound: "Proxy"}, UplinkRate: 4, UplinkTotal: 99},
		{Connection: &daemon.Connection{Id: "b", CreatedAt: 3, Network: "udp", Inbound: "tun", Outbound: "direct"}, ClosedAt: 4, DownlinkRate: 10, DownlinkTotal: 100},
		{Connection: &daemon.Connection{Id: "c", CreatedAt: 2, Network: "tcp", Outbound: "Proxy"}, UplinkRate: 4, UplinkTotal: 99},
	}
	tests := []struct {
		query, filter, sort string
		ids                 string
	}{
		{"", "all", "date", "bca"}, {"", "active", "traffic", "ca"}, {"", "closed", "date", "b"},
		{"", "all", "traffic", "bca"}, {"", "all", "trafficTotal", "bca"},
		{"EXAMPLE network:TCP inbound:ix inbound.type:MIX source:127 destination:1.1 outbound:PROX", "all", "date", "a"},
		{"network:tc", "all", "date", ""}, {"example direct", "all", "date", ""},
		{"1.1.1.1:443", "active", "date", "a"}, {"network:udp", "active", "date", ""},
		{"unknown:key", "all", "date", ""}, {"   ", "all", "date", "bca"},
	}
	for _, tt := range tests {
		t.Run(tt.query+"/"+tt.filter+"/"+tt.sort, func(t *testing.T) {
			got := FilterConnections(rows, tt.query, tt.filter, tt.sort)
			var ids string
			for _, r := range got {
				ids += r.Connection.Id
			}
			if ids != tt.ids {
				t.Fatalf("got %q want %q", ids, tt.ids)
			}
		})
	}
	if rows[0].Connection.Id != "a" {
		t.Fatal("input reordered")
	}
	overflow := FilterConnections([]ConnectionRow{{Connection: &daemon.Connection{Id: "huge"}, UplinkTotal: math.MaxUint64, DownlinkTotal: 1}, {Connection: &daemon.Connection{Id: "small"}, UplinkTotal: 20}}, "", "all", "trafficTotal")
	if overflow[0].Connection.Id != "huge" {
		t.Fatal("sum overflow")
	}
}

func TestFormat(t *testing.T) {
	for _, tt := range []struct{ got, want string }{
		{FormatBytes(0), "0 B"}, {FormatBytes(999), "999 B"}, {FormatBytes(1000), "1 KB"}, {FormatBytes(1500), "1.5 KB"},
		{FormatMemory(1024), "1 KB"}, {FormatMemory(1536), "1.5 KB"}, {FormatBytes(math.MaxUint64), "18.4 EB"},
		{FormatDuration(-time.Second), "0ms"}, {FormatDuration(500 * time.Millisecond), "500ms"},
		{FormatDuration(1500 * time.Millisecond), "1.5s"}, {FormatDuration(12 * time.Second), "12s"},
		{FormatDuration(65 * time.Second), "1m 5s"}, {FormatDuration(125 * time.Minute), "2h 5m"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q want %q", tt.got, tt.want)
		}
	}
}

func TestANSI(t *testing.T) {
	text := "plain \x1b[1;31mred\x1b[3mitalic\x1b[0m normal\x1b[125mblack"
	want := []ANSIRun{{Text: "plain "}, {Text: "red", Color: "#ff2159", Bold: true}, {Text: "italic", Italic: true}, {Text: " normal"}, {Text: "black", Color: "#000000"}}
	if got := ParseANSI(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v", got)
	}
	if got := StripANSI(text); got != "plain reditalic normalblack" {
		t.Fatal(got)
	}
	if len(ParseANSI("\x1b[m\x1b[0m")) != 0 {
		t.Fatal("empty runs")
	}
	if got := ParseANSI("\x1b[1;0;31mx"); !reflect.DeepEqual(got, []ANSIRun{{Text: "x"}}) {
		t.Fatal(got)
	}
	if got := ANSIColor("#000000", "#000000"); got == "#000000" || !strings.HasPrefix(got, "#") {
		t.Fatal(got)
	}
	if ANSIColor("#ffffff", "#000000") != "#ffffff" {
		t.Fatal("changed sufficient contrast")
	}
	if ANSIColor("invalid", "#ffffff") != "invalid" {
		t.Fatal("invalid color")
	}
}
