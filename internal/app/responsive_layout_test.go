package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/core"
	"github.com/xmdhs/boxpilot/internal/storage"
)

func layoutControl(t *testing.T, tt *ui.Tester, role ui.Role, name string, width, height float32) ui.AccessInfo {
	t.Helper()
	info, ok := tt.AccessByRole(role, name)
	if !ok || info.Offscreen || !serverFormContains(ui.Rect{W: width, H: height}, info.Bounds) {
		t.Fatalf("control outside viewport: role=%v name=%q info=%+v", role, name, info)
	}
	return info
}

func TestNarrowPageActionsAndConnectionFilters(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, width := range []int{480, 720, 721, 820, 1140} {
			t.Run(fmt.Sprintf("%s/%d", language, width), func(t *testing.T) {
				a := fixture(t)
				a.settings.Language = language
				a.page = "logs"
				tt := ui.NewTester(a.view, width, 640)
				for _, name := range []string{a.t("暂停更新", "Pause updates"), a.t("复制日志", "Copy logs"), a.t("清除日志", "Clear Logs")} {
					layoutControl(t, tt, ui.RoleButton, name, float32(width), 640)
				}
				layoutControl(t, tt, ui.RolePopUpButton, a.t("服务器", "Servers"), float32(width), 640)
				a.page = "connections"
				tt.Frame()
				search := layoutControl(t, tt, ui.RoleTextField, a.t("搜索", "Search"), float32(width), 640)
				filter := layoutControl(t, tt, ui.RolePopUpButton, a.t("状态", "State"), float32(width), 640)
				layoutControl(t, tt, ui.RolePopUpButton, a.t("排序", "Sort By"), float32(width), 640)
				if narrowPageWidth := float32(width) - 2*pagePadding; width > int(compactBreakpoint) {
					narrowPageWidth -= sidebarWidth
					if narrowPageWidth < actionBreakpoint && filter.Bounds.Y < search.Bounds.Y+search.Bounds.H {
						t.Fatal("narrow filters were not moved below search")
					}
				} else if narrowPageWidth < actionBreakpoint && filter.Bounds.Y < search.Bounds.Y+search.Bounds.H {
					t.Fatal("narrow filters were not moved below search")
				}
			})
		}
	}
}

func TestOverviewOnboardingShowsOnlyWhenNoServersOrProfiles(t *testing.T) {
	for _, language := range []string{"zh-Hans", "en"} {
		t.Run(language, func(t *testing.T) {
			a := fixture(t)
			a.settings.Language = language
			for _, width := range []int{480, 1140} {
				t.Run(fmt.Sprint(width), func(t *testing.T) {
					a.settings.Servers = nil
					a.profiles = nil
					a.page = "overview"
					tt := ui.NewTester(a.view, width, 640)
					remote := a.t("连接远程服务器", "Connect to a remote server")
					local := a.t("使用本机核心", "Set up a local core")
					layoutControl(t, tt, ui.RoleButton, remote, float32(width), 640)
					layoutControl(t, tt, ui.RoleButton, local, float32(width), 640)
					if err := tt.Click(remote); err != nil || !a.serverDialog {
						t.Fatalf("remote onboarding action failed: err=%v dialog=%t", err, a.serverDialog)
					}
					a.serverDialog = false
					tt.Frame()
					if err := tt.Click(local); err != nil || a.page != "core" {
						t.Fatalf("local onboarding action failed: err=%v page=%q", err, a.page)
					}
				})
			}
		})
	}

	for _, configured := range []string{"server", "profile"} {
		a := fixture(t)
		switch configured {
		case "server":
			a.settings.Servers = []storage.Server{{ID: "remote", Name: "Remote", URL: "https://example.com"}}
		case "profile":
			a.profiles = []core.Profile{{ID: "local", Name: "Local", Type: "local"}}
		}
		tt := ui.NewTester(a.view, 800, 640)
		if tt.HasText(a.t("开始使用 BoxPilot", "Get started with BoxPilot")) {
			t.Fatalf("onboarding shown although a %s configuration exists", configured)
		}
	}
}

func TestOverviewCardsWrapByAvailableWidth(t *testing.T) {
	for _, tc := range []struct {
		width   int
		columns int
	}{{480, 1}, {720, 2}, {721, 1}, {820, 1}, {1140, 2}} {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			a := fixture(t)
			a.settings.Servers = []storage.Server{{ID: "remote", Name: "Remote", URL: "https://example.com"}}
			tt := ui.NewTester(a.view, tc.width, 1000)
			upload, ok := tt.Find("Dashboard card: Upload")
			if !ok || upload.W <= 0 {
				t.Fatal("missing upload card")
			}
			download, ok := tt.Find("Dashboard card: Download")
			if !ok || download.W <= 0 {
				t.Fatal("missing download card")
			}
			if !serverFormContains(ui.Rect{W: float32(tc.width), H: 1000}, upload) || !serverFormContains(ui.Rect{W: float32(tc.width), H: 1000}, download) {
				t.Fatalf("cards overflow: upload=%+v download=%+v", upload, download)
			}
			sameRow := upload.Y == download.Y
			if sameRow != (tc.columns == 2) {
				t.Fatalf("wrong column count: want=%d upload=%+v download=%+v", tc.columns, upload, download)
			}
			if tc.columns == 2 && (upload.W < overviewCardWidth || download.W < overviewCardWidth) {
				t.Fatal("two-column cards became narrower than their minimum")
			}
		})
	}
}

func TestGroupsWrapWithinTheirContainer(t *testing.T) {
	for _, width := range []int{480, 721, 820, 1140} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := fixture(t)
			a.page = "groups"
			tt := ui.NewTester(a.view, width, 780)
			for _, name := range []string{"Tokyo", "Singapore", "Direct"} {
				item := layoutControl(t, tt, ui.RoleButton, name, float32(width), 780)
				if item.Bounds.W < groupItemWidth {
					t.Fatalf("node narrower than its minimum: %+v", item.Bounds)
				}
			}
		})
	}
}

func TestRemoteProfileDialogExplainsFieldsAndFitsContent(t *testing.T) {
	a := fixture(t)
	a.profileType = "remote"
	a.profileEditor = true
	a.profileName = "Remote profile"
	a.profileRemoteURL = "https://example.com/config.json"
	tt := ui.NewTester(a.profileForm, 900, 780)
	dialog, ok := tt.AccessByRole(ui.RoleDialog, "")
	if !ok || dialog.Bounds.H >= 560 {
		t.Fatalf("remote profile dialog should fit its contents: %+v", dialog.Bounds)
	}
	for _, label := range []string{
		a.t("本地显示名称", "Local display name"),
		a.t("远程配置 URL", "Remote profile URL"),
	} {
		if !tt.HasText(label) {
			t.Fatalf("remote profile field description is not visible: %q", label)
		}
	}
}

func TestProfileSelectionDoesNotChangeRunningProfile(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a := fixture(t)
			a.settings.Language = language
			a.palette = makePalette(false, "blue")
			a.profiles = []core.Profile{{ID: "running", Name: "Running config", Type: "remote"}, {ID: "next", Name: "Next config", Type: "local"}}
			a.coreProfileID, a.settings.ProfileID = "running", "running"
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.palette.theme)
				ui.Column(c).Fill().Padding(20).Children(func() {
					for _, p := range a.profiles {
						a.profileRow(c, p)
					}
				})
			}, 480, 600)
			selectName := a.t("选择配置", "Select Profile") + ": Next config"
			if err := tt.Click(selectName); err != nil {
				t.Fatal(err)
			}
			if a.settings.ProfileID != "next" || a.coreProfileID != "running" || a.runningProfileName() != "Running config" {
				t.Fatal("selection replaced the actual running profile")
			}
			if a.errorOpen {
				t.Fatal("selecting a profile displayed a notification")
			}
			if !tt.HasText(a.t("运行中", "Running")) || !tt.HasText(a.t("已选择 · 下次启动生效", "Selected · next start")) {
				t.Fatal("running and pending selections are not distinguished")
			}
			for _, p := range a.profiles {
				layoutControl(t, tt, ui.RoleMenuButton, a.t("配置操作", "Profile actions")+": "+p.Name, 480, 600)
			}
			if err := tt.Click(a.t("配置操作", "Profile actions") + ": Running config"); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem(a.t("删除配置", "Delete Profile")); err == nil {
				t.Fatal("running configuration can be deleted")
			}
			tt.CloseMenu()
			if err := tt.Click(a.t("配置操作", "Profile actions") + ": Next config"); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem(a.t("删除配置", "Delete Profile")); err != nil {
				t.Fatal(err)
			}
			if !a.confirmOpen || a.confirmMessage != "Next config" {
				t.Fatal("delete did not require confirmation")
			}
		})
	}
}

func TestDeleteProfileRechecksActualRun(t *testing.T) {
	a := fixture(t)
	p := core.Profile{ID: "profile", Name: "Config"}
	a.coreState.Phase = "stopped"
	a.deleteProfile(p)
	if !a.confirmOpen {
		t.Fatal("missing confirmation")
	}
	a.coreState.Phase, a.coreProfileID = "running", p.ID
	a.settings.ProfileID = "another"
	a.confirmAction()
	if !a.errorOpen || a.busy["delete-profile"] {
		t.Fatal("stale confirmation deleted running profile")
	}
}

func TestCoreManagementCollapsesWithoutHidingDownload(t *testing.T) {
	a := fixture(t)
	a.palette = makePalette(false, "blue")
	a.settings.CorePath = "/test/sing-box"
	a.coreManagerOpen = false
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.palette.theme); a.coreManagementCard(c) }, 480, 600)
	if tt.HasText("Existing binary path") {
		t.Fatal("advanced settings were not collapsed")
	}
	if err := tt.Click("Executable & releases"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Existing binary path") {
		t.Fatal("advanced settings did not open")
	}
	if err := tt.Click("Executable & releases"); err != nil {
		t.Fatal(err)
	}
	a.busy["install"] = true
	canceled := false
	a.jobs["install"] = func() { canceled = true }
	tt.Frame()
	layoutControl(t, tt, ui.RoleButton, "Cancel download", 480, 600)
	if err := tt.Click("Cancel download"); err != nil || !canceled {
		t.Fatalf("cancel inaccessible: %v", err)
	}
}

func TestLogCopyFeedbackReportsFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			a := fixture(t)
			a.page = "logs"
			a.logsQuery = "Started"
			tt := ui.NewTester(a.view, 480, 640)
			if fail {
				tt.FailClipboard(errors.New("clipboard busy"))
			}
			if err := tt.Click("Copy logs"); err != nil {
				t.Fatal(err)
			}
			if fail {
				if !a.errorOpen {
					t.Fatal("failed copy displayed success")
				}
			} else if a.errorOpen || !strings.Contains(tt.Clipboard(), "Started local API") || strings.Contains(tt.Clipboard(), "网络") {
				t.Fatalf("copy should complete silently with filtered content: clipboard=%q", tt.Clipboard())
			}
		})
	}
}
