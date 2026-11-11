package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/xmdhs/boxpilot/internal/core"
)

const defaultRemoteUpdateMinutes = 60

func (a *App) OpenURL(raw string) {
	mygo.RunOnMain(func() {
		if a.win == nil {
			a.pendingOpenURLs = append(a.pendingOpenURLs, raw)
			return
		}
		a.handleOpenURL(raw)
	})
}

func (a *App) handleOpenURL(raw string) {
	name, remoteURL, err := parseRemoteProfileImportLink(raw)
	if err != nil {
		if !errors.Is(err, errUnsupportedOpenURL) {
			a.fail(err)
		}
		return
	}
	remote, err := core.ValidateRemoteURL(remoteURL)
	if err != nil {
		a.fail(err)
		return
	}
	if name == "" {
		name = remote.Host
	}
	message := fmt.Sprintf(a.t("您确定要导入 %s 吗？将会连接到 %s 下载配置。", "Are you sure to import %s? You will connect to %s to download the configuration."), name, remote.Host)
	a.confirm(a.t("导入远程配置", "Import Remote Profile"), message, func() {
		a.newRemoteProfile(name, remoteURL)
	})
}

var errUnsupportedOpenURL = errors.New("unsupported URL")

func parseRemoteProfileImportLink(raw string) (name, remoteURL string, err error) {
	link, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid sing-box import link: %w", err)
	}
	if link.Scheme != "sing-box" || link.Host != "import-remote-profile" {
		return "", "", errUnsupportedOpenURL
	}
	remoteURL = link.Query().Get("url")
	if strings.TrimSpace(remoteURL) == "" {
		return "", "", errors.New("remote profile import link has no URL")
	}
	name = strings.TrimSpace(link.Fragment)
	return name, remoteURL, nil
}

func (a *App) newRemoteProfile(name, remoteURL string) {
	a.profileID = ""
	a.profileType = "remote"
	a.profileName = strings.TrimSpace(name)
	a.profileRemoteURL = remoteURL
	a.profileAutoUpdate = true
	a.profileAutoUpdateInterval = strconv.Itoa(defaultRemoteUpdateMinutes)
	a.profileLastUpdated = time.Time{}
	a.profileContent = ""
	a.profileDirty = false
	a.profileEditor = true
}

func (a *App) saveRemoteProfile() {
	name := tidy(a.profileName)
	remoteURL := tidy(a.profileRemoteURL)
	parsedURL, err := core.ValidateRemoteURL(remoteURL)
	if err != nil {
		a.fail(err)
		return
	}
	if name == "" {
		name = parsedURL.Host
	}
	interval, err := strconv.Atoi(strings.TrimSpace(a.profileAutoUpdateInterval))
	if err != nil || interval <= 0 {
		interval = defaultRemoteUpdateMinutes
	}
	if interval > 10080 {
		a.fail(errors.New("update interval must not exceed 10080 minutes"))
		return
	}
	metadata := core.Profile{
		ID: a.profileID, Name: name, Type: "remote", RemoteURL: parsedURL.String(),
		AutoUpdate: a.profileAutoUpdate, AutoUpdateInterval: interval,
	}
	var existing *core.Profile
	if metadata.ID != "" {
		for i := range a.profiles {
			if a.profiles[i].ID == metadata.ID && a.profiles[i].Type == "remote" {
				profile := a.profiles[i]
				existing = &profile
				break
			}
		}
		if existing == nil {
			a.fail(errors.New("remote profile no longer exists"))
			return
		}
	}
	var saved core.Profile
	binary := a.settings.CorePath
	a.do("save-profile", func(ctx context.Context) error {
		candidate := metadata
		var content string
		if existing == nil {
			var fetchErr error
			content, fetchErr = core.FetchRemoteConfig(ctx, candidate.RemoteURL)
			if fetchErr != nil {
				return fetchErr
			}
			candidate.LastUpdated = time.Now()
		} else if existing.RemoteURL != candidate.RemoteURL {
			var fetchErr error
			content, fetchErr = core.FetchRemoteConfig(ctx, candidate.RemoteURL)
			if fetchErr != nil {
				return fetchErr
			}
			candidate.LastUpdated = time.Now()
		} else {
			var readErr error
			content, readErr = core.ReadProfile(*existing)
			if readErr != nil {
				return readErr
			}
			candidate.LastUpdated = existing.LastUpdated
		}
		if binary != "" && (existing == nil || existing.RemoteURL != candidate.RemoteURL) {
			if err := core.ValidateConfigContent(ctx, binary, content, true); err != nil {
				return err
			}
		}
		var saveErr error
		saved, saveErr = core.SaveProfileWithMetadata(a.root, candidate, name, content)
		return saveErr
	}, func() {
		a.profileEditor = false
		a.profileDirty = false
		a.settings.ProfileID = saved.ID
		a.saveSettings()
		a.refreshLocal()
		a.notice = a.t("完成", "Done")
	})
}

func (a *App) updateRemoteProfile(profile core.Profile) {
	if a.busy["remote-profile-restart"] {
		return
	}
	key := "remote-update-" + profile.ID
	binary := a.settings.CorePath
	var update *core.RemoteProfileUpdate
	a.do(key, func(ctx context.Context) error {
		content, err := core.FetchRemoteConfig(ctx, profile.RemoteURL)
		if err != nil {
			return err
		}
		update, err = core.PrepareRemoteProfileUpdate(a.root, profile, content, time.Now())
		if err != nil {
			return err
		}
		if binary != "" && update.Changed() {
			return update.Validate(ctx, binary, true)
		}
		return nil
	}, func() {
		manager := a.manager
		allowAPIReplace := a.coreAPIReplace
		restart := update.Changed() && manager != nil && a.coreProfileID == profile.ID && a.coreState.Phase == "running"
		jobKey := key
		if restart {
			if a.busy["remote-profile-restart"] || a.busy["core-start"] || a.busy["core-stop"] {
				a.notice = a.t("核心正在切换状态，本次更新尚未应用。", "The core is changing state; this update has not been applied.")
				return
			}
			jobKey = "remote-profile-restart"
		}
		cpuSetIDs, validCPUChoice := a.selectedCPUSetIDs()
		if restart && !validCPUChoice {
			a.fail(errors.New(a.t("核心类型已不可用，请重新选择。", "The selected CPU core type is no longer available.")))
			return
		}
		var updated core.Profile
		a.do(jobKey, func(ctx context.Context) error {
			var err error
			if restart {
				updated, err = update.Restart(ctx, manager, binary, allowAPIReplace, cpuSetIDs)
			} else if err = ctx.Err(); err == nil {
				updated, err = update.Commit()
			}
			return err
		}, func() {
			for i := range a.profiles {
				p := a.profiles[i]
				if p.ID == profile.ID && p.Name == profile.Name && p.RemoteURL == profile.RemoteURL && p.AutoUpdate == profile.AutoUpdate && p.AutoUpdateInterval == profile.AutoUpdateInterval && p.LastUpdated.Equal(profile.LastUpdated) {
					a.profiles[i] = updated
					break
				}
			}
			a.notice = a.t("完成", "Done")
		})
	})
}

func (a *App) updateDueRemoteProfiles() {
	now := time.Now()
	for _, profile := range a.profiles {
		if profile.Type != "remote" || !profile.AutoUpdate || a.busy["remote-update-"+profile.ID] {
			continue
		}
		interval := profile.AutoUpdateInterval
		if interval <= 0 {
			interval = defaultRemoteUpdateMinutes
		}
		if profile.LastUpdated.IsZero() || now.Sub(profile.LastUpdated) >= time.Duration(interval)*time.Minute {
			a.updateRemoteProfile(profile)
		}
	}
}

func (a *App) runRemoteUpdateScheduler() {
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-a.life.Done():
				return
			case <-ticker.C:
				a.post(a.updateDueRemoteProfiles)
			}
		}
	}()
}
