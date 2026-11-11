// Package desktop bridges an application's main-thread state to the native tray.
// It never stops the core or quits mygo.App; shutdown belongs to the application.
package desktop

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// Callbacks execute synchronously on the UI/main thread. Quit should initiate
// the application's asynchronous shutdown. Show is an optional notification
// after the window has been restored/shown/focused; it must not call Show again.
type MenuProfile struct {
	ID       string
	Name     string
	Selected bool
}

type MenuOutbound struct {
	Tag      string
	Delay    int32
	Selected bool
}

type MenuGroup struct {
	Tag        string
	Selected   string
	Selectable bool
	Items      []MenuOutbound
}

type Callbacks struct {
	// EnsureWindow recreates the native UI window if it was destroyed while hidden.
	EnsureWindow func()
	// DestroyWindow releases the native window after tray hiding, while keeping the app alive.
	DestroyWindow  func()
	Show           func()
	Quit           func()
	ToggleCore     func()
	CoreRunning    func() bool
	LocalActive    func() bool
	ActiveName     func() string
	Status         func() string
	Profiles       func() []MenuProfile
	SelectProfile  func(string)
	Groups         func() []MenuGroup
	SelectOutbound func(group, outbound string)
	URLTest        func(tag string)
}

type windowPort interface {
	OnMinimize(func()) func()
	OnClose(func(*mygo.CloseEvent)) func()
	Hide()
	Show()
	Focus()
	Restore()
	IsMinimized() bool
}

type trayPort interface {
	SetMenu(*mygo.Menu)
	SetToolTip(string)
	PopUpMenu()
	OnClick(func()) func()
	OnRightClick(func()) func()
	Destroy()
}

type trayFactory func() (trayPort, error)

// Controller and all its methods (including getters and probe-result setters)
// are main-thread-only. New/Configure must run after App.WhenReady. No method
// starts a goroutine or reads application callbacks from a worker.
type Controller struct {
	win          windowPort
	cb           Callbacks
	tray         trayPort
	makeTray     trayFactory
	platform     string
	hostErr      error
	enabled      bool
	minimize     bool
	close        bool
	quitting     bool
	destroyed    bool
	hidden       bool
	language     string
	menu         *mygo.Menu
	statusItem   *mygo.MenuItem
	showItem     *mygo.MenuItem
	toggleItem   *mygo.MenuItem
	profilesItem *mygo.MenuItem
	groupsItem   *mygo.MenuItem
	quitItem     *mygo.MenuItem
	offMinimize  func()
	offClose     func()
}

var ErrTrayHostNotProbed = errors.New("Linux tray host has not been probed")
var ErrDestroyed = errors.New("desktop controller has been destroyed")

func New(win *mygo.Window, cb Callbacks) *Controller {
	var port windowPort
	if win != nil {
		port = win
	}
	return newController(port, cb, runtime.GOOS, func() (trayPort, error) {
		// Set the image separately: mygo.NewTray does not return its partially
		// constructed tray on an image error, so this permits cleanup.
		t, err := mygo.NewTray(mygo.TrayOptions{ID: "boxpilot-main", ToolTip: "BoxPilot"})
		if err != nil {
			return nil, err
		}
		if err := t.SetIcon(trayIconPNG(32), runtime.GOOS == "darwin"); err != nil {
			t.Destroy()
			return nil, err
		}
		return t, nil
	})
}

func newController(win windowPort, cb Callbacks, platform string, factory trayFactory) *Controller {
	c := &Controller{win: win, cb: cb, platform: platform, makeTray: factory, language: "en"}
	if platform == "linux" {
		c.hostErr = ErrTrayHostNotProbed
	}
	if win != nil {
		c.AttachWindow(win)
	}
	return c
}

// AttachWindow installs tray lifecycle handlers on the current native window.
// It is used when a window destroyed to reclaim UI memory is recreated.
func (c *Controller) AttachWindow(win windowPort) {
	if c.destroyed {
		return
	}
	c.detachWindow()
	c.win = win
	if win != nil {
		c.offMinimize = win.OnMinimize(c.onMinimize)
		c.offClose = win.OnClose(c.onClose)
	}
}

// DetachWindow releases references and event handlers without affecting the tray.
func (c *Controller) DetachWindow() { c.detachWindow() }

func (c *Controller) detachWindow() {
	if c.offMinimize != nil {
		c.offMinimize()
		c.offMinimize = nil
	}
	if c.offClose != nil {
		c.offClose()
		c.offClose = nil
	}
	c.win = nil
}

// SetTrayHostProbe installs a previously computed ProbeTrayHost result. Run the
// probe before App.Run (or on a worker), then deliver its result on the main
// thread before Configure. No D-Bus/process work occurs in this method or
// Configure. Re-probe and supply a new result when the desktop host changes.
// A failed probe removes an existing tray and recovers a window hidden by it.
// Other platforms need no probe and ignore this result.
func (c *Controller) SetTrayHostProbe(err error) {
	if c.destroyed || c.platform != "linux" {
		return
	}
	c.hostErr = err
	if err != nil {
		c.removeTray(true)
	}
}

// Configure creates at most one live tray. Its error is a warning: hiding is
// disabled, minimizing stays normal and closing requests application shutdown.
// Re-enabling after a failure can retry; changing only hide preferences reuses
// the existing tray. Disable removes the tray and recovers a hidden window.
func (c *Controller) Configure(enabled, minimize, close bool) error {
	if c.destroyed {
		return ErrDestroyed
	}
	c.enabled, c.minimize, c.close = enabled, minimize, close
	if !enabled {
		c.removeTray(true)
		return nil
	}
	if c.platform == "linux" && c.hostErr != nil {
		c.removeTray(true)
		return fmt.Errorf("tray unavailable; keeping normal window behavior: %w", c.hostErr)
	}
	if c.tray != nil {
		c.RefreshMenu()
		return nil
	}
	t, err := c.makeTray()
	if err != nil {
		if t != nil {
			t.Destroy()
		}
		c.removeTray(true)
		return fmt.Errorf("tray unavailable; keeping normal window behavior: %w", err)
	}
	if t == nil {
		return errors.New("tray unavailable: factory returned no tray")
	}
	c.tray = t
	c.buildMenu()
	c.RefreshMenu()
	if c.platform == "linux" {
		// AppIndicator requires a permanent menu; no click listener keeps
		// single clicks opening that menu without a double-click delay.
		t.SetMenu(c.menu)
	} else {
		t.OnClick(c.Show)
		t.OnRightClick(func() {
			if !c.Available() {
				return
			}
			c.RefreshMenu()
			// Capture this tray: a menu callback may disable/destroy it while
			// the native popup pumps events. Never touch a replacement tray.
			t.SetMenu(c.menu)
			defer t.SetMenu(nil)
			t.PopUpMenu()
		})
	}
	return nil
}

func (c *Controller) SetQuitting(quitting bool) { c.quitting = quitting }

func (c *Controller) Available() bool {
	return !c.destroyed && c.enabled && c.tray != nil && (c.platform != "linux" || c.hostErr == nil)
}

func (c *Controller) onMinimize() {
	if !c.quitting && c.minimize && c.Available() && c.win != nil {
		c.hideToTray()
	}
}

func (c *Controller) hideToTray() {
	win := c.win
	if win == nil {
		return
	}
	c.hidden = true
	win.Hide()
	if c.cb.DestroyWindow != nil {
		c.detachWindow()
		c.cb.DestroyWindow()
	}
}

func (c *Controller) onClose(e *mygo.CloseEvent) {
	if c.quitting || c.destroyed {
		return
	}
	// Even an ordinary close must let the application clean up its core.
	e.PreventDefault()
	if c.close && c.Available() && c.win != nil {
		c.hideToTray()
		return
	}
	c.requestQuit()
}

func (c *Controller) requestQuit() {
	if !c.quitting && !c.destroyed && c.cb.Quit != nil {
		c.cb.Quit()
	}
}

// Show restores a minimized window, shows it and gives it focus.
func (c *Controller) Show() {
	if c.destroyed {
		return
	}
	if c.win == nil && c.cb.EnsureWindow != nil {
		c.cb.EnsureWindow()
	}
	c.hidden = false
	if c.win != nil {
		if c.win.IsMinimized() {
			c.win.Restore()
		}
		c.win.Show()
		c.win.Focus()
	}
	if c.cb.Show != nil {
		c.cb.Show()
	}
}

func (c *Controller) buildMenu() { c.RefreshMenu() }

// RefreshMenu reads application state only on the calling main thread. Call it
// after local-core, profile, group or active-server state changes.
func (c *Controller) RefreshMenu() {
	if c.destroyed {
		return
	}
	zh := strings.HasPrefix(strings.ToLower(c.language), "zh")
	text := func(en, zhText string) string {
		if zh {
			return zhText
		}
		return en
	}
	running := c.cb.CoreRunning != nil && c.cb.CoreRunning()
	local := c.cb.LocalActive != nil && c.cb.LocalActive()
	label := text("Start local core", "启动本机核心")
	if running {
		label = text("Stop local core", "停止本机核心")
	}
	activeName := text("This Computer", "本机")
	if c.cb.ActiveName != nil && c.cb.ActiveName() != "" {
		activeName = c.cb.ActiveName()
	}
	status := text("Unavailable", "不可用")
	if c.cb.Status != nil && c.cb.Status() != "" {
		status = c.cb.Status()
	}
	c.statusItem = &mygo.MenuItem{Label: activeName, Subtitle: status, Disabled: true}
	c.showItem = &mygo.MenuItem{Label: text("Open BoxPilot", "打开 BoxPilot"), Click: func(*mygo.MenuItem, *mygo.Window) { c.Show() }}
	c.toggleItem = &mygo.MenuItem{Label: label, Disabled: !local || c.cb.ToggleCore == nil || c.quitting, Click: func(*mygo.MenuItem, *mygo.Window) {
		if c.destroyed || c.quitting || c.cb.ToggleCore == nil || c.cb.LocalActive == nil || !c.cb.LocalActive() {
			return
		}
		c.cb.ToggleCore()
		c.RefreshMenu()
	}}
	profiles := []*mygo.MenuItem{}
	if c.cb.Profiles != nil {
		for _, profile := range c.cb.Profiles() {
			profile := profile
			profiles = append(profiles, &mygo.MenuItem{Label: profile.Name, Type: mygo.MenuItemRadio, Checked: profile.Selected,
				Disabled: c.quitting || profile.Selected || c.cb.SelectProfile == nil,
				Click: func(*mygo.MenuItem, *mygo.Window) {
					if !c.destroyed && !c.quitting && c.cb.SelectProfile != nil {
						c.cb.SelectProfile(profile.ID)
						c.RefreshMenu()
					}
				}})
		}
	}
	if len(profiles) == 0 {
		profiles = append(profiles, &mygo.MenuItem{Label: text("Empty profiles", "暂无配置"), Disabled: true})
	}
	c.profilesItem = &mygo.MenuItem{Label: text("Profiles", "配置"), Submenu: profiles}
	items := []*mygo.MenuItem{c.statusItem, mygo.Separator(), c.showItem, c.toggleItem, c.profilesItem}
	groups := []MenuGroup(nil)
	if c.cb.Groups != nil {
		groups = c.cb.Groups()
	}
	if len(groups) > 0 {
		groupItems := make([]*mygo.MenuItem, 0, len(groups))
		for _, group := range groups {
			group := group
			children := make([]*mygo.MenuItem, 0, len(group.Items)+2)
			children = append(children, &mygo.MenuItem{Label: text("Test group", "测试此组"), Disabled: c.quitting || c.cb.URLTest == nil,
				Click: func(*mygo.MenuItem, *mygo.Window) {
					if !c.destroyed && !c.quitting && c.cb.URLTest != nil {
						c.cb.URLTest(group.Tag)
					}
				}}, mygo.Separator())
			for _, outbound := range group.Items {
				outbound := outbound
				label := outbound.Tag
				subtitle := ""
				if outbound.Delay > 0 {
					subtitle = fmt.Sprintf("%d ms", outbound.Delay)
				}
				children = append(children, &mygo.MenuItem{Label: label, Subtitle: subtitle, Type: mygo.MenuItemRadio, Checked: outbound.Selected,
					Disabled: c.quitting || !group.Selectable || outbound.Selected || c.cb.SelectOutbound == nil,
					Click: func(*mygo.MenuItem, *mygo.Window) {
						if !c.destroyed && !c.quitting && group.Selectable && c.cb.SelectOutbound != nil {
							c.cb.SelectOutbound(group.Tag, outbound.Tag)
						}
					}})
			}
			groupItems = append(groupItems, &mygo.MenuItem{Label: group.Tag, Submenu: children})
		}
		c.groupsItem = &mygo.MenuItem{Label: text("Groups", "分组"), Submenu: groupItems}
		items = append(items, c.groupsItem)
	} else {
		c.groupsItem = nil
	}
	c.quitItem = &mygo.MenuItem{Label: text("Quit", "退出"), Disabled: c.quitting, Click: func(*mygo.MenuItem, *mygo.Window) { c.requestQuit() }}
	items = append(items, mygo.Separator(), c.quitItem)
	c.menu = mygo.NewMenu(items)
	if c.tray != nil {
		c.tray.SetToolTip("BoxPilot · " + activeName + " · " + status)
	}
	if c.platform == "linux" && c.tray != nil {
		c.tray.SetMenu(c.menu)
	}
}

func (c *Controller) SetLanguage(language string) {
	c.language = language
	c.RefreshMenu()
}

func (c *Controller) removeTray(recoverWindow bool) {
	if c.tray != nil {
		c.tray.Destroy()
		c.tray = nil
	}
	c.menu, c.showItem, c.toggleItem, c.quitItem = nil, nil, nil, nil
	if recoverWindow && c.hidden && !c.quitting {
		c.Show()
	}
}

// Destroy detaches only this bridge's handlers and releases its tray. It is
// idempotent and does not destroy the window, stop the core or quit the app.
func (c *Controller) Destroy() {
	if c.destroyed {
		return
	}
	c.removeTray(true)
	c.detachWindow()
	c.destroyed = true
}
