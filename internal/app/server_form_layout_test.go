package app

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/storage"
)

func serverFormAccess(t *testing.T, tt *ui.Tester, role ui.Role, name string) ui.AccessInfo {
	t.Helper()
	info, ok := tt.AccessByRole(role, name)
	if !ok {
		t.Fatalf("missing accessible control: role=%v name=%q", role, name)
	}
	return info
}

func serverFormContains(outer, inner ui.Rect) bool {
	const epsilon = float32(.5)
	return inner.W > 0 && inner.H > 0 && inner.X >= outer.X-epsilon && inner.Y >= outer.Y-epsilon &&
		inner.X+inner.W <= outer.X+outer.W+epsilon && inner.Y+inner.H <= outer.Y+outer.H+epsilon
}

func serverFormScroll(t *testing.T, tt *ui.Tester, a *App, dy float32) ui.Rect {
	t.Helper()
	r, ok := tt.Find(a.t("服务器详情", "Server details"))
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("missing form scroll viewport: %+v", r)
	}
	tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, dy)
	return r
}

func TestNewServerDialogFitsContents(t *testing.T) {
	a := fixture(t)
	a.editServer("")
	tt := ui.NewTester(a.serverForm, 1140, 780)
	dialog := serverFormAccess(t, tt, ui.RoleDialog, "").Bounds
	if dialog.H >= 560 {
		t.Fatalf("new server dialog has excessive empty space: %+v", dialog)
	}
}

func TestServerFormSmallWindowLayout(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			for _, editing := range []bool{false, true} {
				for _, remote := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/dark=%t/edit=%t/remote=%t", language, dark, editing, remote), func(t *testing.T) {
						a := fixture(t)
						a.settings.Language = language
						a.palette = makePalette(dark, "blue")
						a.editServer("")
						if editing {
							a.serverID = "existing-server"
						}
						if remote {
							a.serverURL = "http://remote.example:9090"
							a.serverRemember = "file"
						}
						// 单独构建弹窗，不依赖页面标题或其他页面的布局。
						tt := ui.NewTester(a.serverForm, 480, 460)
						panel := serverFormAccess(t, tt, ui.RoleDialog, "").Bounds
						if !serverFormContains(ui.Rect{W: 480, H: 460}, panel) || panel.W > 480*.94+.5 || panel.H > 460*.94+.5 {
							t.Fatalf("dialog exceeds small window: %+v", panel)
						}
						title := a.t("新建服务器", "New Server")
						if editing {
							title = a.t("编辑服务器", "Edit Server")
						}
						titleBefore, ok := tt.Find(title)
						if !ok {
							t.Fatal("missing dialog title")
						}
						buttons := []string{a.t("测试连接", "Test connection"), a.t("取消", "Cancel"), a.t("保存", "Save")}
						if editing {
							buttons = append(buttons, a.t("删除", "Delete"))
						}
						before := map[string]ui.Rect{}
						for _, name := range buttons {
							info := serverFormAccess(t, tt, ui.RoleButton, name)
							if info.Offscreen || !serverFormContains(panel, info.Bounds) {
								t.Fatalf("button %q outside dialog: %+v", name, info)
							}
							before[name] = info.Bounds
						}
						nameBefore, _ := tt.Find(a.t("名称", "Name"))
						viewport := serverFormScroll(t, tt, a, 10000)
						if remote {
							nameAfter, _ := tt.Find(a.t("名称", "Name"))
							// tester 会将完全裁剪的控件边界置零，不能只比较纵坐标。
							if nameBefore.H <= 0 || nameAfter.H > 0 && nameAfter.Y >= nameBefore.Y {
								t.Fatalf("long form did not scroll: before=%+v after=%+v", nameBefore, nameAfter)
							}
							if !tt.HasText(a.t("警告：此地址使用明文 HTTP", "Warning: HTTP may expose your secret")) {
								t.Fatal("missing HTTP warning")
							}
						}
						if !serverFormContains(panel, viewport) || titleBefore.Y+titleBefore.H > viewport.Y+.5 {
							t.Fatalf("invalid header/viewport geometry: title=%+v viewport=%+v", titleBefore, viewport)
						}
						for _, name := range buttons {
							after := serverFormAccess(t, tt, ui.RoleButton, name).Bounds
							if after != before[name] || after.Y < viewport.Y+viewport.H-.5 {
								t.Fatalf("scroll moved or overlapped footer %q: before=%+v after=%+v viewport=%+v", name, before[name], after, viewport)
							}
						}
						if after, _ := tt.Find(title); after != titleBefore {
							t.Fatalf("scroll moved header: before=%+v after=%+v", titleBefore, after)
						}
						if remote {
							for _, name := range []string{
								a.t("我同意将密钥明文保存到本机受限权限文件", "I consent to plaintext storage in a private local file"),
								a.t("我了解并允许此明文连接", "I understand and allow this plaintext connection"),
							} {
								info := serverFormAccess(t, tt, ui.RoleCheckBox, name)
								if info.Offscreen || !serverFormContains(viewport, info.Bounds) {
									t.Fatalf("consent not reachable at end of form: %+v", info)
								}
							}
						}
						if err := tt.Click(a.t("取消", "Cancel")); err != nil || a.serverDialog {
							t.Fatalf("cancel inaccessible: err=%v open=%t", err, a.serverDialog)
						}
					})
				}
			}
		}
	}
}

func TestServerFormSmallWindowConsentAndLabels(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a := fixture(t)
			a.settings.Language = language
			a.editServer("")
			a.serverURL = "http://remote.example:9090"
			a.serverRemember = "file"
			a.serverSecret = "unchanged-secret"
			a.serverID = "layout-consent"
			tt := ui.NewTester(a.serverForm, 480, 460)
			secret := serverFormAccess(t, tt, ui.RoleTextField, a.t("密钥", "Secret"))
			if !secret.Password {
				t.Fatal("secret is not a password field")
			}
			if _, ok := tt.AccessByRole(ui.RoleButton, a.t("连接", "Connect")); ok {
				t.Fatal("ambiguous Connect action remains in form")
			}
			serverFormScroll(t, tt, a, 10000)
			mode := serverFormAccess(t, tt, ui.RolePopUpButton, a.t("保存方式", "Storage mode"))
			if mode.Value != "file" {
				t.Fatalf("storage mode is not distinct from secret: %+v", mode)
			}
			probeName := a.t("测试连接", "Test connection")
			if !serverFormAccess(t, tt, ui.RoleButton, probeName).Disabled {
				t.Fatal("HTTP probe enabled without consent")
			}
			// 保存仍由原有校验拒绝未同意的操作，不额外改变按钮禁用规则。
			if err := tt.Click(a.t("保存", "Save")); err != nil {
				t.Fatal(err)
			}
			if !a.serverDialog || a.busy["save-server"] || a.serverHTTPConsent || a.serverFileConsent ||
				a.errorText != a.t("请先确认明文连接风险", "Confirm the plaintext connection warning first") {
				t.Fatal("save did not enforce HTTP consent")
			}
			if err := tt.Click(a.t("我了解并允许此明文连接", "I understand and allow this plaintext connection")); err != nil {
				t.Fatal(err)
			}
			if !a.serverHTTPConsent || serverFormAccess(t, tt, ui.RoleButton, probeName).Disabled {
				t.Fatal("HTTP consent did not enable probe")
			}
			if err := tt.Click(a.t("保存", "Save")); err != nil {
				t.Fatal(err)
			}
			if !a.serverDialog || a.busy["save-server"] || a.serverFileConsent ||
				a.errorText != a.t("明文密钥存储需要明确同意", "Plaintext credential storage requires consent") {
				t.Fatal("save did not enforce plaintext storage consent")
			}
			if err := tt.Click(a.t("我同意将密钥明文保存到本机受限权限文件", "I consent to plaintext storage in a private local file")); err != nil {
				t.Fatal(err)
			}
			if !a.serverFileConsent || a.serverSecret != "unchanged-secret" {
				t.Fatal("storage consent changed credentials or failed to toggle")
			}
			if err := tt.Click(a.t("保存", "Save")); err != nil {
				t.Fatal(err)
			}
			a.workers.Wait()
			if value, err := storage.LoadSecret(a.root, a.serverID, "file"); err != nil || value != "unchanged-secret" {
				t.Fatalf("consented save did not reach credential worker: value=%q err=%v", value, err)
			}
			// fixture 丢弃主线程回调，事务完成前仍保持忙碌；取消行为不变。
			if !serverFormAccess(t, tt, ui.RoleButton, a.t("保存", "Save")).Disabled {
				t.Fatal("save did not lock while callback is pending")
			}
			if err := tt.Click(a.t("取消", "Cancel")); err != nil || a.serverDialog {
				t.Fatalf("cancel failed: %v", err)
			}
		})
	}
}

func TestServerListSmallWindowActions(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a := fixture(t)
			a.settings.Language = language
			a.palette = makePalette(false, "blue")
			a.settings.Servers = []storage.Server{{
				ID: "layout-list", Name: "A long server name / 一个很长的服务器名称",
				URL: "https://a-long-server-name.example:9090/a/long/api/prefix", Remember: "session",
			}}
			a.page = "settings"
			tt := ui.NewTester(a.view, 480, 460)
			tt.Scroll(240, 230, 0, 10000)
			for _, label := range []string{a.t("连接", "Connect"), a.t("编辑服务器", "Edit Server"), a.t("新建服务器", "New Server")} {
				info := serverFormAccess(t, tt, ui.RoleButton, label)
				if info.Offscreen || !serverFormContains(ui.Rect{W: 480, H: 460}, info.Bounds) {
					t.Fatalf("server list action %q overflows small window: %+v", label, info)
				}
			}
			if err := tt.Click(a.t("编辑服务器", "Edit Server")); err != nil || !a.serverDialog || a.serverID != "layout-list" {
				t.Fatalf("edit action unreachable: err=%v open=%t id=%q", err, a.serverDialog, a.serverID)
			}
		})
	}
}

func TestServerFormSmallWindowDisabledStates(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, state := range []string{"loading", "saving", "storage-blocked", "probing"} {
			t.Run(language+"/"+state, func(t *testing.T) {
				a := fixture(t)
				a.settings.Language = language
				a.editServer("")
				a.serverSecret = "keep-secret"
				a.serverLoading = state == "loading"
				a.storageBlocked = state == "storage-blocked"
				a.busy["save-server"] = state == "saving"
				a.busy["probe"] = state == "probing"
				tt := ui.NewTester(a.serverForm, 480, 460)
				secret := serverFormAccess(t, tt, ui.RoleTextField, a.t("密钥", "Secret"))
				if secret.Disabled != (state == "loading") {
					t.Fatalf("secret disabled state changed: %+v", secret)
				}
				save := serverFormAccess(t, tt, ui.RoleButton, a.t("保存", "Save"))
				if save.Disabled != (state != "probing") {
					t.Fatalf("save disabled state changed: %+v", save)
				}
				probe := serverFormAccess(t, tt, ui.RoleButton, a.t("测试连接", "Test connection"))
				if probe.Disabled != (state == "probing") {
					t.Fatalf("probe disabled state changed: %+v", probe)
				}
				if state == "loading" {
					if err := tt.Click(a.t("密钥", "Secret")); err == nil {
						tt.Type("replacement")
					}
					if a.serverSecret != "keep-secret" {
						t.Fatal("credential loading allowed secret editing")
					}
				}
				if err := tt.Click(a.t("取消", "Cancel")); err != nil || a.serverDialog {
					t.Fatalf("cancel behavior changed during %s: %v", state, err)
				}
			})
		}
	}
}
