package app

import (
	"errors"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"
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
				for _, name := range []string{locales.In(a.language()).PauseUpdates(), locales.In(a.language()).CopyLogs(), locales.In(a.language()).ClearLogs()} {
					layoutControl(t, tt, ui.RoleButton, name, float32(width), 640)
				}
				layoutControl(t, tt, ui.RolePopUpButton, locales.In(a.language()).Servers(), float32(width), 640)
				a.page = "connections"
				tt.Frame()
				search := layoutControl(t, tt, ui.RoleTextField, locales.In(a.language()).Search(), float32(width), 640)
				filter := layoutControl(t, tt, ui.RolePopUpButton, locales.In(a.language()).State(), float32(width), 640)
				layoutControl(t, tt, ui.RolePopUpButton, locales.In(a.language()).SortBy(), float32(width), 640)
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
					remote := locales.In(a.language()).ConnectToARemoteServer()
					local := locales.In(a.language()).SetUpALocalCore()
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
		if tt.HasText(locales.In(a.language()).GetStartedWithBoxPilot()) {
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
		locales.In(a.language()).LocalDisplayName(),
		locales.In(a.language()).RemoteProfileURL(),
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
			selectName := locales.In(a.language()).SelectProfile() + ": Next config"
			if err := tt.Click(selectName); err != nil {
				t.Fatal(err)
			}
			if a.settings.ProfileID != "next" || a.coreProfileID != "running" || a.runningProfileName() != "Running config" {
				t.Fatal("selection replaced the actual running profile")
			}
			if a.errorOpen {
				t.Fatal("selecting a profile displayed a notification")
			}
			if !tt.HasText(locales.In(a.language()).Running()) || !tt.HasText(locales.In(a.language()).SelectedNextStart()) {
				t.Fatal("running and pending selections are not distinguished")
			}
			for _, p := range a.profiles {
				layoutControl(t, tt, ui.RoleMenuButton, locales.In(a.language()).ProfileActions()+": "+p.Name, 480, 600)
			}
			if err := tt.Click(locales.In(a.language()).ProfileActions() + ": Running config"); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem(locales.In(a.language()).DeleteProfile()); err == nil {
				t.Fatal("running configuration can be deleted")
			}
			tt.CloseMenu()
			if err := tt.Click(locales.In(a.language()).ProfileActions() + ": Next config"); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem(locales.In(a.language()).DeleteProfile()); err != nil {
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
