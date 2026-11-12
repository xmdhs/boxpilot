package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/xmdhs/boxpilot/locales"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/core"
	"github.com/xmdhs/boxpilot/internal/desktop"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
	"github.com/xmdhs/boxpilot/internal/model"
	"github.com/xmdhs/boxpilot/internal/storage"
)

const localID = "managed-local"

type localizedCauseError struct {
	message string
	cause   error
}

func (e localizedCauseError) Error() string { return e.message }
func (e localizedCauseError) Unwrap() error { return e.cause }

// App 的可见状态仅在主线程读写，worker 只传递独立结果。
type App struct {
	root                         string
	settings                     storage.Settings
	win                          *mygo.Window
	dispatchMain                 func(func())
	desktop                      *desktop.Controller
	manager                      core.Controller
	life                         context.Context
	cancel                       context.CancelFunc
	workers                      sync.WaitGroup
	closing, cleaned             bool
	storageBlocked               bool
	page                         string
	compactMenu                  bool
	client                       *api.Client
	connectionCancel             context.CancelFunc
	connectionContext            context.Context
	generation                   uint64
	snapshot                     api.Snapshot
	groupExpandOverrides         map[string]groupExpandOverride
	secretCache                  map[string]string
	busy                         map[string]bool
	jobs                         map[string]context.CancelFunc
	errorText                    string
	errorOpen                    bool
	confirmOpen                  bool
	confirmTitle, confirmMessage string
	confirmButtonText            string
	confirmAction                func()
	palette                      palette

	serverDialog                                                  bool
	serverID, serverName, serverURL, serverSecret, serverRemember string
	serverHTTPConsent                                             bool
	serverFileConsent                                             bool
	serverSecretRequired                                          bool
	serverLoading                                                 bool
	serverFormRevision                                            uint64
	connectionQuery                                               string
	connectionList                                                ui.ListState
	detailID                                                      string
	logsQuery, logLevel                                           string
	logsPaused                                                    bool
	frozenLogs                                                    []model.LogEntry
	logList                                                       ui.ListState
	cardDialog                                                    bool

	tool                                           string
	toolURL, toolServer, toolOutbound, toolRuntime string
	toolSerial, toolHTTP3                          bool
	quality                                        *daemon.NetworkQualityTestProgress
	stun                                           *daemon.STUNTestProgress
	toolError                                      string

	installed                              []core.Installed
	profiles                               []core.Profile
	releases                               []core.Release
	releaseChoice, releaseChannel          string
	coreState                              core.State
	coreProfileID                          string
	coreAPIReplace                         bool
	cpuCoreGroups                          []core.CPUCoreGroup
	cpuCoreChoice                          string
	downloadDone, downloadTotal            int64
	profileEditor                          bool
	profileID, profileName, profileContent string
	profileType, profileRemoteURL          string
	profileAutoUpdate                      bool
	profileAutoUpdateInterval              string
	profileLastUpdated                     time.Time
	pendingOpenURLs                        []string
	profileDirty                           bool
	coreManagerOpen                        bool
	externalBinary                         string
	configImportPath                       string
	customAccent                           string
}

func New(root string, s storage.Settings) *App {
	if s.Theme == "" {
		s = storage.Defaults()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{root: root, settings: s, life: ctx, cancel: cancel, page: "overview", secretCache: map[string]string{}, busy: map[string]bool{}, jobs: map[string]context.CancelFunc{}, logLevel: "all", tool: "quality", toolURL: "https://mensura.cdn-apple.com/api/v1/gm/config", toolServer: "stun.voipgate.com:3478", toolRuntime: "20", releaseChannel: "stable", serverRemember: "session", customAccent: s.Accent, cpuCoreChoice: s.CPUCoreChoice}
	if a.cpuCoreChoice == "" {
		a.cpuCoreChoice = "auto"
	}
	a.coreManagerOpen = s.CorePath == ""
	a.logList.FollowEnd = true
	a.connectionList.KeepOffset = true
	return a
}

func (a *App) Open(loadErr error) {
	a.win = mygo.NewWindow(mygo.WindowOptions{Title: "BoxPilot", Width: 1140, Height: 780, MinWidth: 480, MinHeight: 460, StateKey: "main", BackgroundColor: "light-dark(#fafafa, #1e1e1e)", Content: ui.View(a.view)})
	a.desktop = desktop.New(a.win, desktop.Callbacks{
		EnsureWindow: a.ensureWindow, DestroyWindow: a.destroyWindowForTray, Quit: mygo.App.Quit,
		ToggleCore: a.toggleCore, CoreRunning: func() bool { return a.coreState.Phase == "running" },
		LocalActive: func() bool { return a.settings.ActiveID == localID || a.settings.ActiveID == "" },
		ActiveName:  a.activeName, Status: a.phaseLabel,
		Profiles: func() []desktop.MenuProfile {
			profiles := make([]desktop.MenuProfile, 0, len(a.profiles))
			for _, p := range a.profiles {
				profiles = append(profiles, desktop.MenuProfile{ID: p.ID, Name: p.Name, Selected: p.ID == a.settings.ProfileID})
			}
			return profiles
		},
		SelectProfile: func(id string) {
			for _, p := range a.profiles {
				if p.ID == id {
					a.selectProfile(p)
					return
				}
			}
		},
		Groups: func() []desktop.MenuGroup {
			if a.client == nil || a.snapshot.Phase != "active" {
				return nil
			}
			groups := make([]desktop.MenuGroup, 0, len(a.snapshot.Groups))
			for _, g := range a.snapshot.Groups {
				group := desktop.MenuGroup{Tag: g.GetTag(), Selected: g.GetSelected(), Selectable: g.GetSelectable()}
				for _, item := range g.GetItems() {
					group.Items = append(group.Items, desktop.MenuOutbound{Tag: item.GetTag(), Delay: item.GetUrlTestDelay(), Selected: item.GetTag() == g.GetSelected()})
				}
				groups = append(groups, group)
			}
			return groups
		},
		SelectOutbound: func(group, outbound string) {
			a.rpc("tray-select-"+group, func(ctx context.Context, client *api.Client) error {
				return client.SelectOutbound(ctx, group, outbound)
			})
		},
		URLTest: func(tag string) {
			a.rpc("tray-test-"+tag, func(ctx context.Context, client *api.Client) error { return client.URLTest(ctx, tag) })
		},
	})
	a.applyAppearance()
	// 托盘探测和凭据读取可能触及 D-Bus，放在 worker 中。
	var trayProbeErr error
	a.do("startup", func(ctx context.Context) error {
		trayProbeErr = desktop.ProbeTrayHost(ctx)
		return nil
	}, func() {
		a.desktop.SetTrayHostProbe(trayProbeErr)
		a.desktop.SetLanguage(a.language())
		a.configureTray()
	})
	a.replaceCoreController()
	if loadErr != nil {
		a.storageBlocked = true
		a.fail(localizedCauseError{message: locales.In(a.language()).SettingsLoadFailure(loadErr.Error()), cause: loadErr})
	}
	a.refreshLocal()
	a.detectCPUCoreGroups()
	a.runRemoteUpdateScheduler()
	pendingURLs := append([]string(nil), a.pendingOpenURLs...)
	a.pendingOpenURLs = nil
	for _, rawURL := range pendingURLs {
		a.handleOpenURL(rawURL)
	}
	if a.settings.ActiveID != "" && a.settings.ActiveID != localID {
		a.activate(a.settings.ActiveID)
	}
}

func (a *App) replaceCoreController() {
	publish := func(s core.State) {
		a.post(func() {
			a.coreState = s
			if a.desktop != nil {
				a.desktop.RefreshMenu()
			}
			if s.Phase == "running" && a.settings.ActiveID == localID && (a.client == nil || a.snapshot.Phase == "error") {
				a.connect(api.Server{ID: localID, Name: "This Computer", URL: s.URL, Secret: s.Secret})
			}
			if s.Phase == "stopped" || s.Phase == "error" {
				if a.settings.ActiveID == localID {
					a.disconnect()
				}
			}
		})
	}
	if closer, ok := a.manager.(interface{ Close() }); ok {
		closer.Close()
	}
	if a.settings.ElevateCore && core.ElevatedCoreSupported() {
		a.manager = core.NewElevatedManager(a.root, publish)
	} else {
		a.manager = core.NewManager(a.root, publish)
	}
	a.coreState = a.manager.State()
}

func (a *App) ensureWindow() {
	if a.cleaned || a.closing {
		return
	}
	if a.win != nil && !a.win.IsDestroyed() {
		return
	}
	a.win = mygo.NewWindow(mygo.WindowOptions{Title: "BoxPilot", Width: 1140, Height: 780, MinWidth: 480, MinHeight: 460, StateKey: "main", BackgroundColor: "light-dark(#fafafa, #1e1e1e)", Content: ui.View(a.view)})
	if a.desktop != nil {
		a.desktop.AttachWindow(a.win)
	}
	a.coreState = a.manager.State()
}

func (a *App) destroyWindowForTray() {
	win := a.win
	a.win = nil
	if win != nil && !win.IsDestroyed() {
		win.Destroy()
	}
}

func (a *App) post(fn func()) {
	update := func() {
		if !a.cleaned && !a.closing {
			fn()
			// 窗口指针和生命周期只在主线程访问，后台回调不捕获旧窗口。
			if a.win != nil && !a.win.IsDestroyed() {
				a.win.Invalidate()
			}
		}
	}
	// 没有窗口时仍应用后台状态，恢复窗口后即可显示最新数据。
	if a.dispatchMain != nil {
		a.dispatchMain(update)
		return
	}
	mygo.RunOnMain(update)
}

func (a *App) do(key string, task func(context.Context) error, done func()) {
	a.doWithError(key, task, done, nil)
}

func (a *App) doWithError(key string, task func(context.Context) error, done func(), failed func(error)) {
	if a.busy[key] || a.closing {
		return
	}
	ctx, cancel := context.WithCancel(a.life)
	a.jobs[key] = cancel
	a.busy[key] = true
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		err := task(ctx)
		cancel()
		a.post(func() {
			delete(a.jobs, key)
			delete(a.busy, key)
			if err != nil && !errors.Is(err, context.Canceled) {
				if failed != nil {
					failed(err)
				} else {
					a.fail(err)
				}
			} else if err == nil && done != nil {
				done()
			}
		})
	}()
}

func (a *App) rpc(key string, fn func(context.Context, *api.Client) error) {
	a.rpcWithCallbacks(key, fn, nil, nil)
}

func (a *App) rpcWithCallbacks(key string, fn func(context.Context, *api.Client) error, done func(), failed func(error)) {
	c := a.client
	if c == nil {
		err := errors.New(locales.In(a.language()).Connect())
		if failed != nil {
			failed(err)
		} else {
			a.fail(err)
		}
		return
	}
	generation := a.generation
	session := a.connectionContext
	a.doWithError(key, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if session != nil {
			stop := context.AfterFunc(session, cancel)
			defer stop()
		}
		err := fn(ctx, c)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}, func() {
		if generation == a.generation && done != nil {
			done()
		}
	}, func(err error) {
		if generation == a.generation {
			if failed != nil {
				failed(err)
			} else {
				a.fail(err)
			}
		}
	})
}

func (a *App) disconnect() {
	a.generation++
	if a.connectionCancel != nil {
		a.connectionCancel()
		a.connectionCancel = nil
	}
	if a.client != nil {
		a.client.Close()
		a.client = nil
	}
	if cancel := a.jobs["tool"]; cancel != nil {
		cancel()
	}
	a.snapshot = api.Snapshot{}
	a.groupExpandOverrides = nil
	a.detailID = ""
	a.frozenLogs = nil
	a.logsPaused = false
	a.quality = nil
	a.stun = nil
	a.toolError = ""
	a.connectionList = ui.ListState{KeepOffset: true}
	a.logList = ui.ListState{FollowEnd: true}
}

func (a *App) connect(server api.Server) {
	a.disconnect()
	c, err := api.New(server, a.language())
	if err != nil {
		a.fail(err)
		return
	}
	a.client = c
	a.snapshot.Phase = "connecting"
	ctx, cancel := context.WithCancel(a.life)
	a.connectionCancel = cancel
	a.connectionContext = ctx
	generation := a.generation
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		c.Run(ctx, func(s api.Snapshot) {
			a.post(func() {
				if generation == a.generation {
					a.confirmGroupExpansion(s.Groups)
					a.snapshot = s
					if a.desktop != nil {
						a.desktop.RefreshMenu()
					}
				}
			})
		})
	}()
}

func (a *App) activate(id string) {
	a.settings.ActiveID = id
	if a.desktop != nil {
		a.desktop.SetLanguage(a.language())
		a.desktop.RefreshMenu()
	}
	a.saveSettings()
	if id == localID {
		a.disconnect()
		if a.coreState.Phase == "running" {
			a.connect(api.Server{ID: id, URL: a.coreState.URL, Secret: a.coreState.Secret})
		} else {
			a.page = "core"
		}
		return
	}
	for _, s := range a.settings.Servers {
		if s.ID == id {
			server := s
			secret, cached := a.secretCache[id]
			a.disconnect()
			if !cached && s.Remember == "session" {
				a.editServer(id)
				a.serverSecretRequired = true
				return
			}
			if cached {
				a.connect(api.Server{ID: id, Name: s.Name, URL: s.URL, Secret: secret})
				return
			}
			generation := a.generation
			a.do(fmt.Sprintf("credentials-%s-%d", id, generation), func(ctx context.Context) error {
				var err error
				secret, err = storage.LoadSecret(a.root, server.ID, server.Remember)
				return err
			}, func() {
				if a.settings.ActiveID == id && a.generation == generation {
					a.secretCache[id] = secret
					a.connect(api.Server{ID: id, Name: server.Name, URL: server.URL, Secret: secret})
				}
			})
			return
		}
	}
}

func (a *App) saveSettings() {
	if a.storageBlocked || a.closing {
		return
	}
	// 传递深拷贝，避免后台 JSON 编码与 UI 修改切片竞争。
	s := a.settings
	s.Servers = append([]storage.Server(nil), s.Servers...)
	s.Cards = append([]string(nil), s.Cards...)
	s.DisabledCards = append([]string(nil), s.DisabledCards...)
	// 顺序 worker，最新状态不得被先前的写入覆盖。
	a.queueSave(s)
}

func (a *App) fail(err error) {
	if err == nil {
		return
	}
	a.errorText = a.redactSensitive(err.Error())
	a.errorOpen = true
}

func (a *App) redactSensitive(text string) string {
	for _, secret := range a.secretCache {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "••••••")
		}
	}
	if a.coreState.Secret != "" {
		text = strings.ReplaceAll(text, a.coreState.Secret, "••••••")
	}
	if a.serverSecret != "" {
		text = strings.ReplaceAll(text, a.serverSecret, "••••••")
	}
	return text
}

func (a *App) confirm(title, message string, action func()) {
	a.confirmWithButton(title, message, locales.In(a.language()).Ok(), action)
}
func (a *App) confirmWithButton(title, message, button string, action func()) {
	a.confirmTitle = title
	a.confirmMessage = message
	a.confirmButtonText = button
	a.confirmAction = action
	a.confirmOpen = true
}
func (a *App) confirmButtonLabel() string {
	if a.confirmOpen && a.confirmButtonText != "" {
		return a.confirmButtonText
	}
	return locales.In(a.language()).Ok()
}

// WindowAllClosed keeps the MyGo loop alive only while the tray can restore a
// window destroyed to reclaim renderer resources.
func (a *App) WindowAllClosed() {
	if a.desktop == nil || !a.desktop.Available() {
		mygo.App.Quit()
	}
}

func (a *App) Show() {
	if a.desktop != nil {
		a.desktop.Show()
	} else if a.win != nil {
		if a.win.IsMinimized() {
			a.win.Restore()
		}
		a.win.Show()
		a.win.Focus()
	}
}
func (a *App) BeforeQuit(e *mygo.QuitEvent) {
	if a.cleaned {
		if a.desktop != nil {
			a.desktop.SetQuitting(true)
		}
		return
	}
	if e.SessionEnding {
		a.cancel()
		if a.manager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = a.manager.Stop(ctx)
		}
		return
	}
	e.PreventDefault()
	if a.closing {
		return
	}
	a.closing = true
	a.cancel()
	if a.connectionCancel != nil {
		a.connectionCancel()
	}
	if a.win != nil {
		a.win.Invalidate()
	}
	go func() {
		if a.manager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			_ = a.manager.Stop(ctx)
			cancel()
		}
		// 系统文件/凭据对话框不接受 context；不能让它们无限阻止最终退出。
		// 核心已单独完成有界停止，余下任务最多等待三秒，最终关闭父窗口。
		settled := make(chan struct{})
		go func() { a.workers.Wait(); a.flushSaves(); close(settled) }()
		grace := time.NewTimer(3 * time.Second)
		select {
		case <-settled:
		case <-grace.C:
		}
		grace.Stop()
		mygo.RunOnMain(func() {
			a.cleaned = true
			if a.desktop != nil {
				a.desktop.SetQuitting(true)
			}
			mygo.App.Quit()
		})
	}()
}
func (a *App) QuitCanceled() {
	if !a.closing && a.desktop != nil {
		a.desktop.SetQuitting(false)
	}
}
func (a *App) Destroy() {
	a.cancel()
	if closer, ok := a.manager.(interface{ Close() }); ok {
		closer.Close()
	}
	if a.client != nil {
		a.client.Close()
	}
	if a.desktop != nil {
		a.desktop.Destroy()
	}
}

func (a *App) configureTray() {
	if a.desktop != nil {
		if err := a.desktop.Configure(a.settings.TrayEnabled, a.settings.MinimizeToTray, a.settings.CloseToTray); err != nil {
			a.fail(err)
		}
	}
}
func (a *App) applyAppearance() {
	switch a.settings.Theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

func (a *App) refreshLocal() {
	var versions []core.Installed
	var profiles []core.Profile
	a.do("local-files", func(ctx context.Context) error {
		var err error
		versions, err = core.InstalledVersions(a.root)
		if err != nil {
			return err
		}
		profiles, err = core.Profiles(a.root)
		return err
	}, func() {
		a.installed, a.profiles = versions, profiles
		a.updateDueRemoteProfiles()
		if a.desktop != nil {
			a.desktop.RefreshMenu()
		}
	})
}
func (a *App) selectedProfile() (core.Profile, bool) {
	for _, p := range a.profiles {
		if p.ID == a.settings.ProfileID {
			return p, true
		}
	}
	return core.Profile{}, false
}
func (a *App) toggleCore() {
	if a.coreState.Phase == "running" || a.coreState.Phase == "starting" {
		a.stopCore()
	} else {
		a.startCore(false)
	}
}
func (a *App) startCore(replace bool) {
	if a.busy["remote-profile-restart"] {
		a.fail(errors.New(locales.In(a.language()).ApplyingARemoteProfileUpdateTryStartingAgainShortly()))
		return
	}
	if a.busy["cpu-core-detection"] {
		a.fail(errors.New(locales.In(a.language()).DetectingCPUCoreTypesTryStartingTheCoreAgainInAMoment()))
		return
	}
	if a.manager == nil {
		return
	}
	p, ok := a.selectedProfile()
	if !ok {
		a.fail(errors.New(locales.In(a.language()).EmptyProfiles()))
		a.page = "core"
		return
	}
	binary := a.settings.CorePath
	if binary == "" {
		a.fail(errors.New(locales.In(a.language()).CoreIsNotInstalledOrSelectedInstallOrChooseACoreExecutableFirst()))
		a.page = "core"
		return
	}
	cpuSetIDs, validCPUChoice := a.selectedCPUSetIDs()
	if !validCPUChoice {
		a.fail(errors.New(locales.In(a.language()).TheSelectedCPUCoreTypeIsNoLongerAvailableRestartBoxPilotToRefreshDetection()))
		return
	}
	a.activate(localID)
	selectionGeneration := a.generation
	manager := a.manager
	var startErr error
	a.do("core-start", func(ctx context.Context) error {
		startErr = manager.Start(ctx, binary, p, replace, cpuSetIDs)
		return nil
	}, func() {
		if errors.Is(startErr, core.ErrAPIConflict) && !replace {
			a.confirm(locales.In(a.language()).ConfigurationAlreadyHasAnAPIService(), locales.In(a.language()).InTheRuntimeCopyOnlyReplaceExistingTypeApiEntriesWithAnAuthenticated127001APIOnARandomPortDisableDashboardHostingOtherServicesAndTheOriginalFileRemainUnchangedContinue(), func() { a.startCore(true) })
			return
		}
		if startErr != nil {
			if !errors.Is(startErr, context.Canceled) {
				a.fail(startErr)
			}
			return
		}
		a.coreProfileID, a.coreAPIReplace = p.ID, replace
		a.coreStarted(manager.State(), selectionGeneration)
	})
}

func (a *App) coreStarted(s core.State, selectionGeneration uint64) {
	a.coreState = s
	if s.Phase == "running" && a.settings.ActiveID == localID && a.generation == selectionGeneration {
		a.connect(api.Server{ID: localID, URL: s.URL, Secret: s.Secret})
	}
}
func (a *App) stopCore() {
	if a.manager == nil {
		return
	}
	if cancel := a.jobs["remote-profile-restart"]; cancel != nil {
		cancel()
	}
	manager := a.manager
	a.do("core-stop", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return manager.Stop(ctx)
	}, nil)
}

// 设置写入用专用顺序队列；不阻塞原生事件处理。
var saveQueues sync.Map

// 每个 App 拥有独立队列，其生命周期随应用退出结束。
type settingsWrite struct {
	settings  storage.Settings
	committed func() error
	settled   func(error)
}

type saveQueue struct {
	mu      sync.Mutex
	pending []settingsWrite
	running bool
	done    chan struct{}
}

func (a *App) queueSave(s storage.Settings) {
	a.queueSaveCommitted(s, nil)
}

// committed 只在对应设置已持久化后执行，且不依赖可能在退出时被丢弃的 UI 回调。
func (a *App) queueSaveCommitted(s storage.Settings, committed func() error) {
	a.queueSettingsTransaction(s, committed, nil)
}

func (a *App) queueSettingsTransaction(s storage.Settings, committed func() error, settled func(error)) {
	s.Servers = append([]storage.Server(nil), s.Servers...)
	s.Cards = append([]string(nil), s.Cards...)
	s.DisabledCards = append([]string(nil), s.DisabledCards...)
	v, _ := saveQueues.LoadOrStore(a, &saveQueue{})
	q := v.(*saveQueue)
	q.mu.Lock()
	// 普通连续保存仍可合并；带提交动作的写入是不可跳过的事务边界。
	write := settingsWrite{settings: s, committed: committed, settled: settled}
	if n := len(q.pending); n > 0 && committed == nil && settled == nil && q.pending[n-1].committed == nil && q.pending[n-1].settled == nil {
		q.pending[n-1] = write
	} else {
		q.pending = append(q.pending, write)
	}
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.done = make(chan struct{})
	q.mu.Unlock()
	go func() {
		for {
			q.mu.Lock()
			if len(q.pending) == 0 {
				q.running = false
				close(q.done)
				q.mu.Unlock()
				return
			}
			next := q.pending[0]
			q.pending[0] = settingsWrite{}
			q.pending = q.pending[1:]
			q.mu.Unlock()
			err := storage.Save(a.root, next.settings)
			if err == nil && next.committed != nil {
				err = next.committed()
			}
			if next.settled != nil {
				next.settled(err)
			}
			if err != nil {
				a.post(func() { a.fail(err) })
			}
		}
	}()
}
func (a *App) flushSaves() {
	if v, ok := saveQueues.Load(a); ok {
		q := v.(*saveQueue)
		q.mu.Lock()
		done := q.done
		q.mu.Unlock()
		if done != nil {
			<-done
		}
		saveQueues.Delete(a)
	}
}
