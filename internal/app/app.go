package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"boxpilot/internal/api"
	"boxpilot/internal/core"
	"boxpilot/internal/desktop"
	"boxpilot/internal/gen/daemon"
	"boxpilot/internal/model"
	"boxpilot/internal/storage"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const localID = "managed-local"

// App 的可见状态仅在主线程读写，worker 只传递独立结果。
type App struct {
	root                         string
	settings                     storage.Settings
	win                          *mygo.Window
	desktop                      *desktop.Controller
	manager                      *core.Manager
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
	secretCache                  map[string]string
	busy                         map[string]bool
	jobs                         map[string]context.CancelFunc
	errorText                    string
	errorOpen                    bool
	notice                       string
	confirmOpen                  bool
	confirmTitle, confirmMessage string
	confirmAction                func()
	palette                      palette

	serverDialog                                                  bool
	serverID, serverName, serverURL, serverSecret, serverRemember string
	serverHTTPConsent                                             bool
	serverFileConsent                                             bool
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
	downloadDone, downloadTotal            int64
	profileEditor                          bool
	profileID, profileName, profileContent string
	profileDirty                           bool
	externalBinary                         string
	configImportPath                       string
	customAccent                           string
}

func New(root string, s storage.Settings) *App {
	if s.Theme == "" {
		s = storage.Defaults()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{root: root, settings: s, life: ctx, cancel: cancel, page: "overview", secretCache: map[string]string{}, busy: map[string]bool{}, jobs: map[string]context.CancelFunc{}, logLevel: "all", tool: "quality", toolURL: "https://mensura.cdn-apple.com/api/v1/gm/config", toolServer: "stun.voipgate.com:3478", toolRuntime: "20", releaseChannel: "stable", serverRemember: "session", customAccent: s.Accent}
	a.logList.FollowEnd = true
	a.connectionList.KeepOffset = true
	return a
}

func (a *App) Open(loadErr error) {
	a.win = mygo.NewWindow(mygo.WindowOptions{Title: "BoxPilot", Width: 1140, Height: 780, MinWidth: 480, MinHeight: 460, StateKey: "main", BackgroundColor: "light-dark(#fafafa, #1e1e1e)", Content: ui.View(a.view)})
	a.desktop = desktop.New(a.win, desktop.Callbacks{Quit: mygo.App.Quit, ToggleCore: a.toggleCore, CoreRunning: func() bool { return a.coreState.Phase == "running" }, LocalActive: func() bool { return a.settings.ActiveID == localID || a.settings.ActiveID == "" }})
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
	a.manager = core.NewManager(a.root, func(s core.State) {
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
	})
	if loadErr != nil {
		a.storageBlocked = true
		a.fail(fmt.Errorf("读取设置失败；为保护原文件，已暂停保存：%w", loadErr))
	}
	a.refreshLocal()
	if a.settings.ActiveID != "" && a.settings.ActiveID != localID {
		a.activate(a.settings.ActiveID)
	}
}

func (a *App) post(fn func()) {
	if a.win != nil {
		a.win.Update(func() {
			if !a.cleaned && !a.closing {
				fn()
			}
		})
	}
}

func (a *App) do(key string, task func(context.Context) error, done func()) {
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
				a.fail(err)
			} else if err == nil && done != nil {
				done()
			}
		})
	}()
}

func (a *App) rpc(key string, fn func(context.Context, *api.Client) error) {
	c := a.client
	if c == nil {
		a.fail(errors.New(a.t("请先连接实例", "Connect to an instance first")))
		return
	}
	generation := a.generation
	session := a.connectionContext
	a.do(key, func(ctx context.Context) error {
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
		if generation == a.generation {
			a.notice = a.t("操作完成", "Operation completed")
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
					a.snapshot = s
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
				a.notice = a.t("该服务器的密钥仅保存于上次会话，请重新输入。", "This server's secret was session-only. Please enter it again.")
				a.editServer(id)
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
	a.confirmTitle = title
	a.confirmMessage = message
	a.confirmAction = action
	a.confirmOpen = true
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
			a.notice = err.Error()
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
	}, func() { a.installed = versions; a.profiles = profiles })
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
	if a.manager == nil {
		return
	}
	p, ok := a.selectedProfile()
	if !ok {
		a.fail(errors.New(a.t("请创建并选择配置", "Create and select a configuration")))
		a.page = "core"
		return
	}
	binary := a.settings.CorePath
	if binary == "" {
		a.fail(errors.New(a.t("请先安装或选择核心", "Install or select a core first")))
		a.page = "core"
		return
	}
	a.activate(localID)
	selectionGeneration := a.generation
	var startErr error
	a.do("core-start", func(ctx context.Context) error {
		startErr = a.manager.Start(ctx, binary, p, replace)
		return nil
	}, func() {
		if errors.Is(startErr, core.ErrAPIConflict) && !replace {
			a.confirm(a.t("配置中已有 API 服务", "Configuration already has an API service"), a.t("仅在运行副本中：将已有 type=api 条目替换为随机密钥、随机端口的 127.0.0.1 API，并禁用 dashboard。其他服务和原配置文件保持不变。是否继续？", "In the runtime copy only: replace existing type=api entries with an authenticated 127.0.0.1 API on a random port; disable dashboard hosting. Other services and the original file remain unchanged. Continue?"), func() { a.startCore(true) })
			return
		}
		if startErr != nil {
			if !errors.Is(startErr, context.Canceled) {
				a.fail(startErr)
			}
			return
		}
		a.coreStarted(a.manager.State(), selectionGeneration)
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
	a.do("core-stop", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return a.manager.Stop(ctx)
	}, nil)
}

// 设置写入用专用顺序队列；不阻塞原生事件处理。
var saveQueues sync.Map

// 每个 App 拥有独立队列，其生命周期随应用退出结束。
type saveQueue struct {
	mu      sync.Mutex
	latest  *storage.Settings
	running bool
	done    chan struct{}
}

func (a *App) queueSave(s storage.Settings) {
	v, _ := saveQueues.LoadOrStore(a, &saveQueue{})
	q := v.(*saveQueue)
	q.mu.Lock()
	q.latest = &s
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
			next := q.latest
			q.latest = nil
			if next == nil {
				q.running = false
				close(q.done)
				q.mu.Unlock()
				return
			}
			q.mu.Unlock()
			if err := storage.Save(a.root, *next); err != nil {
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

func (a *App) exportLogs() {
	entries := append([]model.LogEntry(nil), a.visibleLogs()...)
	var text strings.Builder
	for _, e := range entries {
		text.WriteString(a.redactSensitive(plainLog(e.Message)))
		text.WriteByte('\n')
	}
	data := text.String()
	a.do("export-logs", func(ctx context.Context) error {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "Export logs", DefaultPath: "boxpilot-logs.txt"})
		if err != nil {
			return err
		}
		if path == "" {
			return nil
		}
		return os.WriteFile(filepath.Clean(path), []byte(data), 0600)
	}, nil)
}
