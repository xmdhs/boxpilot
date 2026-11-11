package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/core"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
	"github.com/xmdhs/boxpilot/internal/model"
	"github.com/xmdhs/boxpilot/internal/storage"
)

func fixture(t *testing.T) *App {
	t.Helper()
	s := storage.Defaults()
	s.Language = "en"
	s.Theme = "light"
	s.ActiveID = localID
	a := New(t.TempDir(), s)
	// Unit tests have no MyGo event loop; drop UI callbacks instead of blocking
	// workers in RunOnMain. Tests that need callback effects can dispatch them explicitly.
	a.dispatchMain = func(func()) {}
	t.Cleanup(func() { a.cancel(); a.workers.Wait(); a.flushSaves() })
	a.client = &api.Client{}
	a.snapshot = api.Snapshot{Phase: "active", Version: "1.14.4", APIVersion: 4, StartedAt: time.Now().Add(-time.Hour).UnixMilli(), ServiceStatus: &daemon.ServiceStatus{Status: daemon.ServiceStatus_STARTED}, Status: &daemon.Status{Memory: 45 << 20, Goroutines: 42, ConnectionsIn: 12, ConnectionsOut: 8, TrafficAvailable: true, Uplink: 420000, Downlink: 3400000, UplinkTotal: 125000000, DownlinkTotal: 920000000}, UploadHistory: []float64{1, 4, 2, 8, 6, 10, 4}, DownloadHistory: []float64{8, 4, 6, 3, 8, 12, 9}, Modes: []string{"rule", "global", "direct"}, CurrentMode: "rule", Groups: []*daemon.Group{{Tag: "Proxy", Type: "selector", Selectable: true, Selected: "Tokyo", IsExpand: true, Items: []*daemon.GroupItem{{Tag: "Tokyo", Type: "shadowsocks", UrlTestDelay: 46}, {Tag: "Singapore", Type: "vless", UrlTestDelay: 82}, {Tag: "Direct", Type: "direct"}}}}, Connections: []model.ConnectionRow{{Connection: &daemon.Connection{Id: "connection-1", Network: "tcp", Domain: "example.com", Destination: "93.184.216.34:443", Inbound: "mixed-in", InboundType: "mixed", Outbound: "Tokyo", CreatedAt: time.Now().Add(-time.Minute).UnixMilli()}, UplinkRate: 2048, DownlinkRate: 4096, UplinkTotal: 8000, DownlinkTotal: 16000}}, Logs: []model.LogEntry{{ID: 1, Level: daemon.LogLevel_INFO, Message: "Started local API"}, {ID: 2, Level: daemon.LogLevel_WARN, Message: "\x1b[33m网络连接重试\x1b[0m"}}, StreamErrors: map[string]string{}}
	a.coreState = core.State{Phase: "running", Version: "1.14.4", PID: 1234}
	return a
}
func TestAllNativePages(t *testing.T) {
	for _, page := range pages {
		t.Run(page.id, func(t *testing.T) {
			a := fixture(t)
			a.page = page.id
			tt := ui.NewTester(a.view, 1140, 780)
			if !tt.HasText(page.en) {
				t.Fatalf("page %s missing: %q", page.id, tt.Texts())
			}
			if tt.Image().Bounds().Dx() != 1140 {
				t.Fatal("unexpected image size")
			}
		})
	}
}
func TestNavigationAndCompactMenu(t *testing.T) {
	a := fixture(t)
	tt := ui.NewTester(a.view, 1140, 780)
	if err := tt.Click("Logs"); err != nil {
		t.Fatal(err)
	}
	if a.page != "logs" || !tt.HasText("Started local API") {
		t.Fatalf("logs page: %q", tt.Texts())
	}
	tt.SetSize(600, 760)
	if err := tt.Click("Toggle navigation"); err != nil {
		t.Fatal(err)
	}
	if !a.compactMenu {
		t.Fatal("compact navigation did not open")
	}
	tt.Key(0, ui.KeyEscape)
	if a.compactMenu {
		t.Fatal("Escape did not close navigation")
	}
}
func TestConnectionsSearchAndDetails(t *testing.T) {
	a := fixture(t)
	a.page = "connections"
	tt := ui.NewTester(a.view, 1140, 780)
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tt.Type("network:udp")
	if !tt.HasText("No matching connections") {
		t.Fatalf("search not applied: %q", tt.Texts())
	}
	a.connectionQuery = ""
	tt.Frame()
	if err := tt.Click("connection-1"); err != nil {
		t.Fatal(err)
	}
	if a.detailID != "connection-1" || !tt.HasText("Metadata") {
		t.Fatal("connection detail not opened")
	}
	tt.Key(0, ui.KeyEscape)
	if a.detailID != "" {
		t.Fatal("detail did not close")
	}
}
func TestLogPauseFreezesBoundedSnapshot(t *testing.T) {
	a := fixture(t)
	a.page = "logs"
	tt := ui.NewTester(a.view, 1140, 780)
	if err := tt.Click("Pause updates"); err != nil {
		t.Fatal(err)
	}
	a.snapshot.Logs = append(a.snapshot.Logs, model.LogEntry{ID: 3, Level: daemon.LogLevel_INFO, Message: "new message"})
	tt.Frame()
	if tt.HasText("new message") {
		t.Fatal("paused view changed")
	}
	if err := tt.Click("Resume updates"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("new message") {
		t.Fatal("resume did not display current buffer")
	}
}
func TestServerConsent(t *testing.T) {
	a := fixture(t)
	a.editServer("")
	a.serverURL = "http://192.168.1.2:9090/prefix"
	if _, err := a.formServer(); err == nil {
		t.Fatal("plaintext remote accepted without consent")
	}
	a.serverHTTPConsent = true
	s, err := a.formServer()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(s.URL, "/prefix") {
		t.Fatal("base path discarded")
	}
	a.serverURL = "http://127.0.0.1:9090"
	a.serverHTTPConsent = false
	if _, err = a.formServer(); err != nil {
		t.Fatal(err)
	}
}
func TestANSIAndUnicodeHighlight(t *testing.T) {
	a := fixture(t)
	a.palette = makePalette(false, "blue")
	text := "before \x1b[31m网络连接\x1b[0m after\x1b]8;;https://example.com\x07hidden\x1b]8;;\x07"
	if got := plainLog(text); got != "before 网络连接 afterhidden" {
		t.Fatalf("plain: %q", got)
	}
	spans := a.logSpans(text, "连接")
	highlight := false
	for _, s := range spans {
		if s.Text == "连接" && s.Background.A > 0 {
			highlight = true
		}
	}
	if !highlight {
		t.Fatal("Unicode search not highlighted")
	}
}
func TestThemeAndNarrowLayouts(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		a := fixture(t)
		a.settings.Theme = theme
		for _, size := range [][2]int{{480, 640}, {720, 700}, {1140, 780}} {
			tt := ui.NewTester(a.view, size[0], size[1])
			if !tt.HasText("Overview") {
				t.Fatal("overview missing")
			}
			if a.palette.dark != (theme == "dark") {
				t.Fatal("wrong theme")
			}
		}
	}
}
func TestModalBackdropDimsWindow(t *testing.T) {
	a := fixture(t)
	tt := ui.NewTester(a.view, 1140, 780)
	before, _, _, _ := tt.Image().At(10, 10).RGBA()
	a.serverDialog = true
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	after, _, _, _ := tt.Image().At(10, 10).RGBA()
	if after >= before {
		t.Fatalf("modal backdrop did not dim the window: before=%d after=%d", before, after)
	}
}

func TestGroupExpansionOverrideRendersImmediatelyAndConfirms(t *testing.T) {
	a := fixture(t)
	a.page = "groups"
	group := a.snapshot.Groups[0]
	a.groupExpandOverrides = map[string]groupExpandOverride{
		group.GetTag(): {expanded: false, generation: a.generation},
	}

	tt := ui.NewTester(a.view, 1140, 780)
	if !tt.HasText("Expand") || tt.HasText("Tokyo") {
		t.Fatalf("optimistic collapse was not rendered: %q", tt.Texts())
	}

	// An older snapshot must not discard a pending UI choice.
	a.confirmGroupExpansion(a.snapshot.Groups)
	if a.groupExpanded(group) {
		t.Fatal("stale snapshot overrode the pending collapse")
	}

	group.IsExpand = false
	a.confirmGroupExpansion(a.snapshot.Groups)
	if _, ok := a.groupExpandOverrides[group.GetTag()]; ok {
		t.Fatal("matching snapshot did not confirm collapse")
	}
}

func TestGroupExpansionRollbackOnlyRemovesMatchingRequest(t *testing.T) {
	a := fixture(t)
	a.groupExpandOverrides = map[string]groupExpandOverride{"Proxy": {expanded: false, generation: 3}}
	a.rollbackGroupExpansion("Proxy", 2, false)
	if _, ok := a.groupExpandOverrides["Proxy"]; !ok {
		t.Fatal("stale RPC failure removed a newer override")
	}
	a.rollbackGroupExpansion("Proxy", 3, false)
	if _, ok := a.groupExpandOverrides["Proxy"]; ok {
		t.Fatal("matching RPC failure did not roll back the override")
	}
}

func TestNativeSnapshots(t *testing.T) {
	dir := os.Getenv("BOXPILOT_SCREENSHOTS")
	if dir == "" {
		t.Skip("设置 BOXPILOT_SCREENSHOTS 可输出内存绘制截图；文字仍需 Pango/Cairo")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"light", "dark"} {
		for _, page := range pages {
			a := fixture(t)
			a.settings.Theme = theme
			a.page = page.id
			tt := ui.NewTester(a.view, 1140, 780)
			f, err := os.Create(filepath.Join(dir, theme+"-"+page.id+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
