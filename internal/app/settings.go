package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/storage"
)

func (a *App) settingsView(c *ui.Context) {
	a.pageTitle(c, locales.In(a.language()).Settings(), nil)
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(18).Children(func() {
			a.card(c, locales.In(a.language()).Appearance(), func() {
				a.setting(c, locales.In(a.language()).Theme(), "", func() {
					themeValues := []string{"auto", "light", "dark"}
					themeOptions := []string{locales.In(a.language()).System(), locales.In(a.language()).Light(), locales.In(a.language()).Dark()}
					themeOption := themeOptions[0]
					for i, value := range themeValues {
						if a.settings.Theme == value {
							themeOption = themeOptions[i]
						}
					}
					if transitionedSelect(c, &themeOption, themeOptions).Width(150).Label(locales.In(a.language()).Theme()).Changed() {
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
					locales.In(a.language()).Default(), locales.In(a.language()).Blue(), locales.In(a.language()).Purple(), locales.In(a.language()).Pink(),
					locales.In(a.language()).Red(), locales.In(a.language()).Orange(), locales.In(a.language()).Yellow(), locales.In(a.language()).Green(),
					locales.In(a.language()).Graphite(), locales.In(a.language()).CustomColor(),
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
				a.setting(c, locales.In(a.language()).CustomColor(), "", func() {
					if transitionedSelect(c, &accentOption, accentOptions).Width(150).Label(locales.In(a.language()).CustomColor()).Changed() {
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
					if ui.ColorPicker(c, &color).Label(locales.In(a.language()).CustomColor()).Changed() {
						a.settings.Accent = fmt.Sprintf("#%02x%02x%02x", color.R, color.G, color.B)
						a.saveSettings()
					}
				}
				a.setting(c, locales.In(a.language()).Language(), "", func() {
					languageValues := mygo.App.Languages()
					languageOptions := make([]string, len(languageValues))
					languageOption := ""
					for i, tag := range languageValues {
						languageOptions[i] = i18n.Autonym(tag)
						if tag == a.language() {
							languageOption = languageOptions[i]
						}
					}
					if transitionedSelect(c, &languageOption, languageOptions).Width(150).Label(locales.In(a.language()).Language()).Changed() {
						for i, option := range languageOptions {
							if languageOption == option {
								a.setLanguage(languageValues[i])
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
			a.card(c, locales.In(a.language()).App(), func() {
				a.setting(c, locales.In(a.language()).EnableTray(), "", func() {
					if transitionedSwitch(c, &a.settings.TrayEnabled).Label(locales.In(a.language()).EnableTray()).Changed() {
						a.configureTray()
						a.saveSettings()
					}
				})
				a.setting(c, locales.In(a.language()).MinimizeToTray(), "", func() {
					if transitionedSwitch(c, &a.settings.MinimizeToTray).Label(locales.In(a.language()).MinimizeToTray()).Changed() {
						a.configureTray()
						a.saveSettings()
					}
				})
				a.setting(c, locales.In(a.language()).KeepTrayInBackground(), "", func() {
					if transitionedSwitch(c, &a.settings.CloseToTray).Label(locales.In(a.language()).KeepTrayInBackground()).Changed() {
						a.configureTray()
						a.saveSettings()
					}
				})
				a.setting(c, locales.In(a.language()).OpenAtLogin(), "", func() {
					enabled := a.settings.OpenAtLogin
					if transitionedSwitch(c, &enabled).Disabled(a.busy["login"]).Label(locales.In(a.language()).OpenAtLogin()).Changed() {
						next := enabled
						a.do("login", func(context.Context) error { return mygo.App.SetOpenAtLogin(next) }, func() { a.settings.OpenAtLogin = next; a.saveSettings() })
					}
				})
			})
			a.card(c, locales.In(a.language()).Servers(), func() {
				for _, s := range a.settings.Servers {
					s := s
					ui.Row(c.Key(s.ID)).FillWidth().Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
						ui.Column(c).Grow(1).MinWidth(180).MaxWidthPercent(100).Gap(4).Children(func() {
							ui.Text(c, s.Name).FillWidth()
							a.label(c, s.URL).FillWidth().Selectable()
						})
						transitionedButton(c, locales.In(a.language()).Connect()).OnClick(func() { a.activate(s.ID); a.page = "overview" })
						transitionedButton(c, locales.In(a.language()).EditServer()).OnClick(func() { a.editServer(s.ID) })
					})
				}
				transitionedButton(c, locales.In(a.language()).NewServer()).OnClick(func() { a.editServer("") })
			})
			a.card(c, locales.In(a.language()).About(), func() {
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
	_, height := c.Size()
	ui.DialogBase(c, &a.serverDialog, func(back, panel ui.Element) {
		animateDialog(back, panel)
		back.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(480).MaxWidthPercent(94).MaxHeight(620).MaxHeightPercent(94).Padding(24).Radius(20).Background(a.palette.card).Gap(14)
		serverTitle := locales.In(a.language()).NewServer()
		if a.serverID != "" {
			serverTitle = locales.In(a.language()).EditServer()
		}
		ui.Text(c, serverTitle).FontSize(20).Bold().FillWidth().Shrink(0)
		// 标题和操作区不参与收缩，长文案只占用中间的滚动区域。
		ui.Scroll(c.Key("server-form-fields")).MaxHeight(min(400, height-200)).MinHeight(0).FillWidth().Label(locales.In(a.language()).ServerDetails()).Children(func() {
			ui.Column(c).FillWidth().Gap(14).Shrink(0).Children(func() {
				a.field(c, locales.In(a.language()).Name(), func() {
					ui.TextInput(c.Key("server-name"), &a.serverName).FillWidth().Label(locales.In(a.language()).Name())
				})
				a.field(c, locales.In(a.language()).Url(), func() {
					ui.TextInput(c.Key("server-url"), &a.serverURL).FillWidth().Label(locales.In(a.language()).Url())
				})
				a.field(c, locales.In(a.language()).Secret(), func() {
					ui.TextInput(c.Key("server-secret"), &a.serverSecret).Password().Disabled(a.serverLoading).FillWidth().Label(locales.In(a.language()).Secret())
				})
				a.field(c, locales.In(a.language()).StorageMode(), func() {
					transitionedSelect(c, &a.serverRemember, []string{"session", "keychain", "file"}).FillWidth().Label(locales.In(a.language()).StorageMode())
				})
				a.label(c, locales.In(a.language()).SessionThisSessionKeychainSystemCredentialStoreFilePlaintext()).FillWidth()
				if a.serverRemember == "file" {
					transitionedCheckbox(c, &a.serverFileConsent, locales.In(a.language()).IConsentToPlaintextStorageInAPrivateLocalFile()).FillWidth()
				}
				if insecureRemote(a.serverURL) {
					ui.Text(c, locales.In(a.language()).WarningHTTPMayExposeYourSecretToNetworkObservers()).TextColor(a.palette.danger).FontSize(12).FillWidth()
					transitionedCheckbox(c, &a.serverHTTPConsent, locales.In(a.language()).IUnderstandAndAllowThisPlaintextConnection()).FillWidth()
				}
			})
		})
		// 分成两行，避免小窗口内测试连接、删除与保存操作互相挤压。
		ui.Column(c).FillWidth().Gap(10).Shrink(0).Children(func() {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				if a.serverID != "" {
					transitionedButton(c, locales.In(a.language()).Delete()).TextColor(a.palette.danger).OnClick(func() {
						a.confirm(locales.In(a.language()).Delete(), a.serverName, func() { a.deleteServer() })
					})
				}
				transitionedButton(c, locales.In(a.language()).TestConnection()).Disabled(a.busy["probe"] || insecureRemote(a.serverURL) && !a.serverHTTPConsent).OnClick(a.probeServer)
			})
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.Spacer(c)
				transitionedButton(c, locales.In(a.language()).Cancel()).OnClick(func() { a.serverDialog = false })
				transitionedPrimaryButton(c, locales.In(a.language()).Save()).Disabled(a.busy["save-server"] || a.serverLoading || a.storageBlocked).OnClick(a.saveServer)
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
		return api.Server{}, errors.New(locales.In(a.language()).EnterAValidHttpSAPIURLWithoutUserInfoQueryOrFragment())
	}
	if insecureRemote(address) && !a.serverHTTPConsent {
		return api.Server{}, errors.New(locales.In(a.language()).ConfirmThePlaintextConnectionWarningFirst())
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
	}, nil)
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
		a.fail(errors.New(locales.In(a.language()).PlaintextCredentialStorageRequiresConsent()))
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
					return localizedCauseError{message: locales.In(a.language()).CredentialsSavedCleanupFailure(err.Error()), cause: err}
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
