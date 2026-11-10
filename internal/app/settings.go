package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"boxpilot/internal/api"
	"boxpilot/internal/storage"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *App) settingsView(c *ui.Context) {
	a.pageTitle(c, "设置", "Settings", nil)
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(18).Children(func() {
			a.card(c, a.t("外观与语言", "Appearance & language"), func() {
				a.setting(c, a.t("主题", "Theme"), "", func() {
					if ui.Select(c, &a.settings.Theme, []string{"auto", "light", "dark"}).Width(150).Label(a.t("主题", "Theme")).Changed() {
						if a.win != nil {
							a.applyAppearance()
						}
						a.saveSettings()
					}
				})
				a.setting(c, a.t("强调色", "Accent"), "", func() {
					choice := a.settings.Accent
					if strings.HasPrefix(choice, "#") {
						choice = "custom"
					}
					if ui.Select(c, &choice, accents).Width(150).Label(a.t("强调色", "Accent")).Changed() {
						if choice == "custom" {
							a.customAccent = "#007aff"
							a.settings.Accent = a.customAccent
						} else {
							a.settings.Accent = choice
						}
						a.saveSettings()
					}
				})
				if strings.HasPrefix(a.settings.Accent, "#") {
					color := ui.Hex(a.settings.Accent)
					if ui.ColorPicker(c, &color).Label(a.t("自定义强调色", "Custom accent")).Changed() {
						a.settings.Accent = fmt.Sprintf("#%02x%02x%02x", color.R, color.G, color.B)
						a.saveSettings()
					}
				}
				a.setting(c, a.t("界面语言", "Language"), "", func() {
					language := a.settings.Language
					if language == "" {
						language = "zh-Hans"
					}
					if ui.Select(c, &language, []string{"zh-Hans", "en"}).Width(150).Label(a.t("界面语言", "Language")).Changed() {
						a.settings.Language = language
						a.saveSettings()
						if a.settings.ActiveID != "" {
							a.activate(a.settings.ActiveID)
						}
					}
				})
			})
			a.card(c, a.t("桌面与托盘", "Desktop & tray"), func() {
				a.setting(c, a.t("启用托盘", "Enable tray"), a.t("不可用时保留普通窗口行为", "Normal window behavior is kept if unavailable"), func() {
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
				a.setting(c, a.t("关闭窗口时隐藏", "Close to tray"), a.t("通过托盘或“退出应用”完全退出", "Quit explicitly from the tray or sidebar"), func() {
					if ui.Switch(c, &a.settings.CloseToTray).Label(a.t("关闭窗口时隐藏", "Close to tray")).Changed() {
						a.configureTray()
						a.saveSettings()
					}
				})
				a.setting(c, a.t("开机启动", "Open at login"), a.t("仅启动面板，不自动启动网络配置", "Starts the panel, not a network configuration"), func() {
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
					ui.Row(c.Key(s.ID)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(4).Children(func() { ui.Text(c, s.Name); a.label(c, s.URL).Selectable() })
						ui.Button(c, a.t("连接", "Connect")).OnClick(func() { a.activate(s.ID); a.page = "overview" })
						ui.Button(c, a.t("编辑", "Edit")).OnClick(func() { a.editServer(s.ID) })
					})
				}
				ui.Button(c, a.t("添加服务器", "Add server")).OnClick(func() { a.editServer("") })
			})
			a.card(c, a.t("关于", "About"), func() {
				ui.Text(c, "BoxPilot 0.1.0")
				a.label(c, a.t("独立原生客户端 · GPL-3.0-or-later · MyGo (MIT)", "Independent native client · GPL-3.0-or-later · MyGo (MIT)"))
				a.label(c, a.t("兼容 sing-box 1.14+ StartedService API；不是 Clash REST 客户端。", "Uses sing-box 1.14+ StartedService API, not Clash REST."))
				a.label(c, a.t("本机核心默认不提权，不自动修改系统代理或安装系统服务。", "No elevation, automatic system proxy changes, or system service installation."))
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
		panel.Width(480).MaxWidthPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(14)
		ui.Text(c, a.t("连接服务器", "Connect to server")).FontSize(20).Bold()
		a.field(c, a.t("名称", "Name"), func() {
			ui.TextInput(c.Key("server-name"), &a.serverName).FillWidth().Label(a.t("服务器名称", "Server name"))
		})
		a.field(c, "API URL", func() { ui.TextInput(c.Key("server-url"), &a.serverURL).FillWidth().Label("API URL") })
		a.field(c, a.t("密钥", "Secret"), func() {
			ui.TextInput(c.Key("server-secret"), &a.serverSecret).Password().Disabled(a.serverLoading).FillWidth().Label(a.t("密钥", "Secret"))
		})
		a.field(c, a.t("密钥存储", "Secret storage"), func() {
			ui.Select(c, &a.serverRemember, []string{"session", "keychain", "file"}).FillWidth().Label(a.t("密钥存储", "Secret storage"))
		})
		a.label(c, a.t("session：仅本次；keychain：系统凭据库；file：明文文件。", "session: this session; keychain: system credential store; file: plaintext."))
		if a.serverRemember == "file" {
			ui.Checkbox(c, &a.serverFileConsent, a.t("我同意将密钥明文保存到本机受限权限文件", "I consent to plaintext storage in a private local file"))
		}
		if insecureRemote(a.serverURL) {
			ui.Text(c, a.t("警告：此地址使用明文 HTTP，密钥可能被网络窃听。", "Warning: HTTP may expose your secret to network observers.")).TextColor(a.palette.danger).FontSize(12)
			ui.Checkbox(c, &a.serverHTTPConsent, a.t("我了解并允许此明文连接", "I understand and allow this plaintext connection"))
		}
		ui.Row(c).Gap(10).Wrap().Children(func() {
			if a.serverID != "" {
				ui.Button(c, a.t("删除服务器", "Delete server")).TextColor(a.palette.danger).OnClick(func() {
					a.confirm(a.t("删除服务器？", "Delete server?"), a.serverName, func() { a.deleteServer() })
				})
			}
			ui.Button(c, a.t("测试连接", "Test connection")).Disabled(a.busy["probe"] || insecureRemote(a.serverURL) && !a.serverHTTPConsent).OnClick(a.probeServer)
			ui.Spacer(c)
			ui.Button(c, a.t("取消", "Cancel")).OnClick(func() { a.serverDialog = false })
			ui.PrimaryButton(c, a.t("保存", "Save")).Disabled(a.busy["save-server"] || a.serverLoading || a.storageBlocked).OnClick(a.saveServer)
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
	}, func() { a.notice = a.t("API 连接成功", "API connection succeeded") })
}
func (a *App) saveServer() {
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
		if oldMode != "" && oldMode != mode {
			if err := storage.DeleteSecret(a.root, server.ID, oldMode); err != nil {
				_ = storage.DeleteSecret(a.root, server.ID, mode)
				return fmt.Errorf("无法移除旧凭据存储，未切换保存模式：%w", err)
			}
		}
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
		a.saveSettings()
		a.activate(server.ID)
		a.page = "overview"
	})
}
func (a *App) deleteServer() {
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
