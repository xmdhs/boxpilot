package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/storage"
)

func (a *App) settingsView(c *ui.Context) {
	a.pageTitle(c, "设置", "Settings", nil)
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(18).Children(func() {
			a.card(c, a.t("外观", "Appearance"), func() {
				a.setting(c, a.t("主题", "Theme"), "", func() {
					themeValues := []string{"auto", "light", "dark"}
					themeOptions := []string{a.t("跟随系统", "System"), a.t("浅色", "Light"), a.t("深色", "Dark")}
					themeOption := themeOptions[0]
					for i, value := range themeValues {
						if a.settings.Theme == value {
							themeOption = themeOptions[i]
						}
					}
					if ui.Select(c, &themeOption, themeOptions).Width(150).Label(a.t("主题", "Theme")).Changed() {
						for i, option := range themeOptions {
							if themeOption == option {
								a.settings.Theme = themeValues[i]
								if a.win != nil {
									a.applyAppearance()
								}
								a.saveSettings()
								break
							}
						}
					}
				})
				accentValues := []string{"default", "blue", "purple", "pink", "red", "orange", "yellow", "green", "graphite", "custom"}
				accentOptions := []string{
					a.t("默认", "Default"), a.t("蓝色", "Blue"), a.t("紫色", "Purple"), a.t("粉色", "Pink"),
					a.t("红色", "Red"), a.t("橙色", "Orange"), a.t("黄色", "Yellow"), a.t("绿色", "Green"),
					a.t("石墨色", "Graphite"), a.t("自定义颜色", "Custom color"),
				}
				accentValue := a.settings.Accent
				if strings.HasPrefix(accentValue, "#") {
					accentValue = "custom"
				}
				if !includes(accentValues, accentValue) {
					accentValue = "default"
				}
				accentOption := accentOptions[0]
				for i, value := range accentValues {
					if value == accentValue {
						accentOption = accentOptions[i]
					}
				}
				a.setting(c, a.t("自定义颜色", "Custom color"), "", func() {
					if ui.Select(c, &accentOption, accentOptions).Width(150).Label(a.t("自定义颜色", "Custom color")).Changed() {
						for i, option := range accentOptions {
							if accentOption == option {
								accentValue = accentValues[i]
								if accentValue == "custom" {
									a.customAccent = "#007aff"
									a.settings.Accent = a.customAccent
								} else {
									a.settings.Accent = accentValue
								}
								a.saveSettings()
								break
							}
						}
					}
				})
				if strings.HasPrefix(a.settings.Accent, "#") {
					color := ui.Hex(a.settings.Accent)
					if ui.ColorPicker(c, &color).Label(a.t("自定义颜色", "Custom color")).Changed() {
						a.settings.Accent = fmt.Sprintf("#%02x%02x%02x", color.R, color.G, color.B)
						a.saveSettings()
					}
				}
				a.setting(c, a.t("语言", "Language"), "", func() {
					languageValues := []string{"zh-Hans", "en"}
					languageOptions := []string{"简体中文", "English"}
					languageOption := languageOptions[0]
					if a.settings.Language == "en" {
						languageOption = languageOptions[1]
					}
					if ui.Select(c, &languageOption, languageOptions).Width(150).Label(a.t("语言", "Language")).Changed() {
						for i, option := range languageOptions {
							if languageOption == option {
								a.settings.Language = languageValues[i]
								a.saveSettings()
								if a.settings.ActiveID != "" {
									a.activate(a.settings.ActiveID)
								}
								break
							}
						}
					}
				})
			})
			a.card(c, a.t("应用", "App"), func() {
				a.setting(c, a.t("启用托盘", "Enable tray"), "", func() {
					if ui.Switch(c, &a.settings.TrayEnabled).Label(a.t("启用托盘", "Enable tray")).Changed() {
						a.configureTray()
						a.saveSettings()
					}
				})
				a.setting(c, a.t("最小化到托盘", "Minimize to tray"), "", func() {
					if ui.Switch(c, &a.settings.MinimizeToTray).Label(a.t("最小化到托盘", "Minimize to tray")).Changed() {
						a.configureTray()
						a.saveSettings()
					}
				})
				a.setting(c, a.t("将托盘保留在后台", "Keep Tray in Background"), "", func() {
					if ui.Switch(c, &a.settings.CloseToTray).Label(a.t("将托盘保留在后台", "Keep Tray in Background")).Changed() {
						a.configureTray()
						a.saveSettings()
					}
				})
				a.setting(c, a.t("开机启动", "Open at login"), "", func() {
					enabled := a.settings.OpenAtLogin
					if ui.Switch(c, &enabled).Disabled(a.busy["login"]).Label(a.t("开机启动", "Open at login")).Changed() {
						next := enabled
						a.do("login", func(context.Context) error { return mygo.App.SetOpenAtLogin(next) }, func() { a.settings.OpenAtLogin = next; a.saveSettings() })
					}
				})
			})
			a.card(c, a.t("服务器", "Servers"), func() {
				for _, s := range a.settings.Servers {
					s := s
					ui.Row(c.Key(s.ID)).FillWidth().Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
						ui.Column(c).Grow(1).MinWidth(180).MaxWidthPercent(100).Gap(4).Children(func() {
							ui.Text(c, s.Name).FillWidth()
							a.label(c, s.URL).FillWidth().Selectable()
						})
						ui.Button(c, a.t("连接", "Connect")).OnClick(func() { a.activate(s.ID); a.page = "overview" })
						ui.Button(c, a.t("编辑服务器", "Edit Server")).OnClick(func() { a.editServer(s.ID) })
					})
				}
				ui.Button(c, a.t("新建服务器", "New Server")).OnClick(func() { a.editServer("") })
			})
			a.card(c, a.t("关于", "About"), func() {
				ui.Text(c, "BoxPilot 0.1.0")
			})
		})
	})
}
func (a *App) editServer(id string) {
	a.serverFormRevision++
	revision := a.serverFormRevision
	a.serverLoading = false
	a.serverID = id
	a.serverName = ""
	a.serverURL = "http://127.0.0.1:9090"
	a.serverSecret = ""
	a.serverSecretRequired = false
	a.serverRemember = "session"
	a.serverHTTPConsent = false
	a.serverFileConsent = false
	for _, s := range a.settings.Servers {
		if s.ID == id {
			a.serverName = s.Name
			a.serverURL = s.URL
			a.serverRemember = s.Remember
			a.serverSecret = a.secretCache[id]
			a.serverFileConsent = s.Remember == "file"
			if a.serverSecret == "" && s.Remember != "session" {
				server := s
				var secret string
				var loadErr error
				a.serverLoading = true
				a.do(fmt.Sprintf("edit-credential-%d", revision), func(context.Context) error {
					secret, loadErr = storage.LoadSecret(a.root, server.ID, server.Remember)
					return nil
				}, func() {
					if a.serverDialog && a.serverID == server.ID && a.serverFormRevision == revision {
						a.serverLoading = false
						if loadErr != nil {
							a.fail(loadErr)
							return
						}
						a.serverSecret = secret
						a.secretCache[server.ID] = secret
					}
				})
			}
		}
	}
	a.serverDialog = true
}
func (a *App) serverForm(c *ui.Context) {
	ui.DialogBase(c, &a.serverDialog, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(480).MaxWidthPercent(94).Height(620).MaxHeightPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(14)
		serverTitle := a.t("新建服务器", "New Server")
		if a.serverID != "" {
			serverTitle = a.t("编辑服务器", "Edit Server")
		}
		ui.Text(c, serverTitle).FontSize(20).Bold().FillWidth().Shrink(0)
		// 标题和操作区不参与收缩，长文案只占用中间的滚动区域。
		ui.Scroll(c.Key("server-form-fields")).Grow(1).MinHeight(0).FillWidth().Label(a.t("服务器详情", "Server details")).Children(func() {
			ui.Column(c).FillWidth().Gap(14).Shrink(0).Children(func() {
				a.field(c, a.t("名称", "Name"), func() {
					ui.TextInput(c.Key("server-name"), &a.serverName).FillWidth().Label(a.t("名称", "Name"))
				})
				a.field(c, a.t("URL", "URL"), func() { ui.TextInput(c.Key("server-url"), &a.serverURL).FillWidth().Label(a.t("URL", "URL")) })
				a.field(c, a.t("密钥", "Secret"), func() {
					ui.TextInput(c.Key("server-secret"), &a.serverSecret).Password().Disabled(a.serverLoading).FillWidth().Label(a.t("密钥", "Secret"))
				})
				a.field(c, a.t("保存方式", "Storage mode"), func() {
					ui.Select(c, &a.serverRemember, []string{"session", "keychain", "file"}).FillWidth().Label(a.t("保存方式", "Storage mode"))
				})
				a.label(c, a.t("session：仅本次；keychain：系统凭据库；file：明文文件。", "session: this session; keychain: system credential store; file: plaintext.")).FillWidth()
				if a.serverRemember == "file" {
					ui.Checkbox(c, &a.serverFileConsent, a.t("我同意将密钥明文保存到本机受限权限文件", "I consent to plaintext storage in a private local file")).FillWidth()
				}
				if insecureRemote(a.serverURL) {
					ui.Text(c, a.t("警告：此地址使用明文 HTTP，密钥可能被网络窃听。", "Warning: HTTP may expose your secret to network observers.")).TextColor(a.palette.danger).FontSize(12).FillWidth()
					ui.Checkbox(c, &a.serverHTTPConsent, a.t("我了解并允许此明文连接", "I understand and allow this plaintext connection")).FillWidth()
				}
			})
		})
		// 分成两行，避免小窗口内测试连接、删除与保存操作互相挤压。
		ui.Column(c).FillWidth().Gap(10).Shrink(0).Children(func() {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				if a.serverID != "" {
					ui.Button(c, a.t("删除", "Delete")).TextColor(a.palette.danger).OnClick(func() {
						a.confirm(a.t("删除", "Delete"), a.serverName, func() { a.deleteServer() })
					})
				}
				ui.Button(c, a.t("测试连接", "Test connection")).Disabled(a.busy["probe"] || insecureRemote(a.serverURL) && !a.serverHTTPConsent).OnClick(a.probeServer)
			})
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.Spacer(c)
				ui.Button(c, a.t("取消", "Cancel")).OnClick(func() { a.serverDialog = false })
				ui.PrimaryButton(c, a.t("保存", "Save")).Disabled(a.busy["save-server"] || a.serverLoading || a.storageBlocked).OnClick(a.saveServer)
			})
		})
	})
}
func insecureRemote(address string) bool {
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}
func (a *App) formServer() (api.Server, error) {
	address := tidy(a.serverURL)
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return api.Server{}, errors.New(a.t("请输入有效的 http(s) API 地址，不要包含用户名、查询参数或片段", "Enter a valid http(s) API URL without user info, query, or fragment"))
	}
	if insecureRemote(address) && !a.serverHTTPConsent {
		return api.Server{}, errors.New(a.t("请先确认明文连接风险", "Confirm the plaintext connection warning first"))
	}
	name := tidy(a.serverName)
	if name == "" {
		name = u.Host
	}
	return api.Server{ID: a.serverID, Name: name, URL: strings.TrimRight(address, "/"), Secret: a.serverSecret}, nil
}
func (a *App) probeServer() {
	server, err := a.formServer()
	if err != nil {
		a.fail(err)
		return
	}
	language := a.language()
	a.do("probe", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		client, err := api.New(server, language)
		if err != nil {
			return err
		}
		defer client.Close()
		return client.Probe(ctx)
	}, func() { a.notice = a.t("可用", "Available") })
}
func (a *App) saveServer() {
	if a.storageBlocked || a.closing {
		return
	}
	server, err := a.formServer()
	if err != nil {
		a.fail(err)
		return
	}
	mode := a.serverRemember
	if mode == "file" && !a.serverFileConsent {
		a.fail(errors.New(a.t("明文密钥存储需要明确同意", "Plaintext credential storage requires consent")))
		return
	}
	if server.ID == "" {
		server.ID = storage.GenerateID()
	}
	oldMode := ""
	for _, s := range a.settings.Servers {
		if s.ID == server.ID {
			oldMode = s.Remember
		}
	}
	a.do("save-server", func(context.Context) error {
		if err := storage.SaveSecret(a.root, server.ID, server.Secret, mode); err != nil {
			return err
		}
		// 保留旧存储直到新的 Remember 设置真正落盘；退出丢弃回调也不丢旧密钥。
		return nil
	}, func() {
		a.secretCache[server.ID] = server.Secret
		entry := storage.Server{ID: server.ID, Name: server.Name, URL: server.URL, Remember: mode}
		found := false
		for i, s := range a.settings.Servers {
			if s.ID == entry.ID {
				a.settings.Servers[i] = entry
				found = true
			}
		}
		if !found {
			a.settings.Servers = append(a.settings.Servers, entry)
		}
		a.serverDialog = false
		// 直到落盘及旧凭据清理完成前，禁止再次迁移到可能被清理的存储。
		a.busy["save-server"] = true
		a.queueSettingsTransaction(a.settings, func() error {
			if oldMode != "" && oldMode != mode {
				if err := storage.DeleteSecret(a.root, server.ID, oldMode); err != nil {
					return fmt.Errorf("新凭据和设置已保存，但清理旧凭据失败：%w", err)
				}
			}
			return nil
		}, func(error) {
			a.post(func() { delete(a.busy, "save-server") })
		})
		a.activate(server.ID)
		a.page = "overview"
	})
}
func (a *App) deleteServer() {
	if a.busy["save-server"] || a.storageBlocked || a.closing {
		return
	}
	id := a.serverID
	mode := "session"
	for _, server := range a.settings.Servers {
		if server.ID == id {
			mode = server.Remember
			break
		}
	}
	a.do("delete-server", func(context.Context) error { return storage.DeleteSecret(a.root, id, mode) }, func() {
		var servers []storage.Server
		for _, s := range a.settings.Servers {
			if s.ID != id {
				servers = append(servers, s)
			}
		}
		a.settings.Servers = servers
		delete(a.secretCache, id)
		a.serverDialog = false
		if a.settings.ActiveID == id {
			a.settings.ActiveID = localID
			a.disconnect()
		}
		a.saveSettings()
	})
}
