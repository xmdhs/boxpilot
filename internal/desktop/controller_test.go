package desktop

import (
	"errors"
	"reflect"
	"testing"

	"github.com/egoist/mygo"
)

type fakeWindow struct {
	minimize  func()
	close     func(*mygo.CloseEvent)
	minimized bool
	actions   []string
}

func (w *fakeWindow) OnMinimize(fn func()) func() {
	w.minimize = fn
	return func() { w.minimize = nil }
}
func (w *fakeWindow) OnClose(fn func(*mygo.CloseEvent)) func() {
	w.close = fn
	return func() { w.close = nil }
}
func (w *fakeWindow) Hide()  { w.actions = append(w.actions, "hide") }
func (w *fakeWindow) Show()  { w.actions = append(w.actions, "show") }
func (w *fakeWindow) Focus() { w.actions = append(w.actions, "focus") }
func (w *fakeWindow) Restore() {
	w.minimized = false
	w.actions = append(w.actions, "restore")
}
func (w *fakeWindow) IsMinimized() bool { return w.minimized }

type fakeTray struct {
	menu             *mygo.Menu
	click, right     func()
	menus            []*mygo.Menu
	destroys, popups int
	duringPopup      func()
}

func (t *fakeTray) SetMenu(menu *mygo.Menu) {
	t.menu = menu
	t.menus = append(t.menus, menu)
}
func (t *fakeTray) PopUpMenu() {
	t.popups++
	if t.duringPopup != nil {
		t.duringPopup()
	}
}
func (t *fakeTray) OnClick(fn func()) func() {
	t.click = fn
	return func() { t.click = nil }
}
func (t *fakeTray) OnRightClick(fn func()) func() {
	t.right = fn
	return func() { t.right = nil }
}
func (t *fakeTray) Destroy() { t.destroys++ }

func TestControllerCloseMinimizePolicy(t *testing.T) {
	for _, platform := range []string{"linux", "windows", "darwin"} {
		for _, enabled := range []bool{false, true} {
			for _, minimize := range []bool{false, true} {
				for _, closeToTray := range []bool{false, true} {
					for _, quitting := range []bool{false, true} {
						w, tray := &fakeWindow{}, &fakeTray{}
						quits := 0
						c := newController(w, Callbacks{Quit: func() { quits++ }}, platform, func() (trayPort, error) { return tray, nil })
						c.SetTrayHostProbe(nil)
						if err := c.Configure(enabled, minimize, closeToTray); err != nil {
							t.Fatal(err)
						}
						c.SetQuitting(quitting)
						w.minimize()
						wantHide := !quitting && enabled && minimize
						if (len(w.actions) > 0) != wantHide {
							t.Fatalf("minimize %s enabled=%v minimize=%v quitting=%v: %v", platform, enabled, minimize, quitting, w.actions)
						}
						w.actions = nil
						e := &mygo.CloseEvent{}
						w.close(e)
						wantHide = !quitting && enabled && closeToTray
						wantQuit := !quitting && !wantHide
						if e.DefaultPrevented() != !quitting || (len(w.actions) > 0) != wantHide || (quits == 1) != wantQuit {
							t.Fatalf("close %s enabled=%v close=%v quitting=%v: prevented=%v actions=%v quits=%d", platform, enabled, closeToTray, quitting, e.DefaultPrevented(), w.actions, quits)
						}
						c.SetQuitting(true)
						c.Destroy()
					}
				}
			}
		}
	}
}

func TestLinuxRequiresVerifiedHostAndHandlesHostLoss(t *testing.T) {
	w, tray := &fakeWindow{}, &fakeTray{}
	creates, quits := 0, 0
	c := newController(w, Callbacks{Quit: func() { quits++ }}, "linux", func() (trayPort, error) { creates++; return tray, nil })
	if err := c.Configure(true, true, true); !errors.Is(err, ErrTrayHostNotProbed) || creates != 0 || c.Available() {
		t.Fatalf("unprobed host enabled hiding: %v, creates=%d", err, creates)
	}
	w.minimize()
	e := &mygo.CloseEvent{}
	w.close(e)
	if len(w.actions) != 0 || !e.DefaultPrevented() || quits != 1 {
		t.Fatalf("unavailable tray did not preserve ordinary behavior: %v, %d", w.actions, quits)
	}
	c.SetTrayHostProbe(ErrTrayHostUnavailable)
	if err := c.Configure(true, true, true); !errors.Is(err, ErrTrayHostUnavailable) {
		t.Fatal(err)
	}
	c.SetTrayHostProbe(nil)
	if err := c.Configure(true, true, true); err != nil || !c.Available() {
		t.Fatalf("verified host unavailable: %v", err)
	}
	if tray.menu == nil || tray.click != nil || tray.right != nil {
		t.Fatal("Linux must have a permanent menu and no direct-click override")
	}
	w.minimize()
	w.actions = nil
	c.SetTrayHostProbe(ErrTrayHostUnavailable)
	if c.Available() || tray.destroys != 1 || !reflect.DeepEqual(w.actions, []string{"show", "focus"}) {
		t.Fatalf("host loss stranded hidden window: available=%v destroys=%d actions=%v", c.Available(), tray.destroys, w.actions)
	}
}

func TestTrayCreationFailureIsWarningAndCanRetry(t *testing.T) {
	w, tray := &fakeWindow{}, &fakeTray{}
	creates, quits := 0, 0
	failure := errors.New("native tray missing")
	c := newController(w, Callbacks{Quit: func() { quits++ }}, "windows", func() (trayPort, error) {
		creates++
		if creates == 1 {
			return tray, failure
		}
		return tray, nil
	})
	if err := c.Configure(true, true, true); !errors.Is(err, failure) || c.Available() || tray.destroys != 1 {
		t.Fatalf("failed creation: %v, available=%v, destroys=%d", err, c.Available(), tray.destroys)
	}
	w.minimize()
	w.close(&mygo.CloseEvent{})
	if len(w.actions) != 0 || quits != 1 {
		t.Fatal("creation failure allowed hide or skipped application shutdown")
	}
	if err := c.Configure(true, true, true); err != nil || !c.Available() || creates != 2 {
		t.Fatalf("retry failed: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := c.Configure(true, i%2 == 0, true); err != nil {
			t.Fatal(err)
		}
	}
	if creates != 2 {
		t.Fatal("preference changes recreated an existing tray")
	}
	c.SetQuitting(true)
	c.Destroy()
}

func TestDisableRecoversWindowAndDestroyDetaches(t *testing.T) {
	w, tray := &fakeWindow{minimized: true}, &fakeTray{}
	shown, creates := 0, 0
	c := newController(w, Callbacks{Show: func() { shown++ }}, "darwin", func() (trayPort, error) { creates++; return tray, nil })
	if err := c.Configure(true, true, true); err != nil {
		t.Fatal(err)
	}
	w.minimize()
	w.actions = nil
	if err := c.Configure(false, true, true); err != nil {
		t.Fatal(err)
	}
	if c.Available() || tray.destroys != 1 || shown != 1 || !reflect.DeepEqual(w.actions, []string{"restore", "show", "focus"}) {
		t.Fatalf("disable did not recover: %v shown=%d destroys=%d", w.actions, shown, tray.destroys)
	}
	if err := c.Configure(true, false, false); err != nil || creates != 2 {
		t.Fatalf("reenable: %v creates=%d", err, creates)
	}
	c.Destroy()
	c.Destroy()
	if w.minimize != nil || w.close != nil || c.Available() || tray.destroys != 2 {
		t.Fatal("Destroy must detach its handlers and destroy each tray only once")
	}
	if err := c.Configure(true, true, true); !errors.Is(err, ErrDestroyed) {
		t.Fatal(err)
	}
	w.actions = nil
	c.Show()
	if len(w.actions) != 0 {
		t.Fatal("destroyed bridge showed window")
	}
}

func TestPrimaryClickShowsAndRightClickTemporaryMenu(t *testing.T) {
	for _, platform := range []string{"darwin", "windows"} {
		w, tray := &fakeWindow{minimized: true}, &fakeTray{}
		c := newController(w, Callbacks{}, platform, func() (trayPort, error) { return tray, nil })
		if err := c.Configure(true, true, true); err != nil {
			t.Fatal(err)
		}
		if tray.menu != nil || tray.click == nil || tray.right == nil {
			t.Fatal("permanent menu consumed primary click")
		}
		tray.click()
		if !reflect.DeepEqual(w.actions, []string{"restore", "show", "focus"}) {
			t.Fatalf("click did not restore/show/focus: %v", w.actions)
		}
		tray.duringPopup = func() {
			if tray.menu == nil {
				t.Fatal("popup did not install menu")
			}
		}
		tray.right()
		if tray.popups != 1 || tray.menu != nil || len(tray.menus) != 2 || tray.menus[1] != nil {
			t.Fatal("popup left a persistent menu")
		}
		// Disabling in a native popup must not panic or restore a stale menu.
		tray.duringPopup = func() {
			if err := c.Configure(false, true, true); err != nil {
				t.Fatal(err)
			}
		}
		tray.right()
		if c.Available() || tray.menu != nil || tray.destroys != 1 {
			t.Fatal("popup reentrancy left a tray/menu installed")
		}
		c.Destroy()
	}
}

func TestMenuStateLanguageAndRemoteSafety(t *testing.T) {
	tray := &fakeTray{}
	running, local := false, true
	toggles, quits, shown := 0, 0, 0
	c := newController(&fakeWindow{}, Callbacks{
		CoreRunning: func() bool { return running }, LocalActive: func() bool { return local },
		ToggleCore: func() { toggles++; running = !running },
		Quit:       func() { quits++ }, Show: func() { shown++ },
	}, "linux", func() (trayPort, error) { return tray, nil })
	c.SetTrayHostProbe(nil)
	if err := c.Configure(true, true, true); err != nil {
		t.Fatal(err)
	}
	if c.toggleItem.Label != "Start local core" || c.toggleItem.Disabled {
		t.Fatal("incorrect initial core menu")
	}
	c.toggleItem.Click(nil, nil)
	if toggles != 1 || c.toggleItem.Label != "Stop local core" {
		t.Fatal("core menu failed to refresh after toggle")
	}
	local = false // stale enabled menu activation must still be guarded
	c.toggleItem.Click(nil, nil)
	if toggles != 1 {
		t.Fatal("remote selection toggled local core")
	}
	c.RefreshMenu()
	if !c.toggleItem.Disabled {
		t.Fatal("remote selection did not disable local-core action")
	}
	c.SetLanguage("zh-CN")
	if c.toggleItem.Label != "停止本机核心" || c.quitItem.Label != "退出" {
		t.Fatal("language was not applied")
	}
	c.showItem.Click(nil, nil)
	c.quitItem.Click(nil, nil)
	if shown != 1 || quits != 1 {
		t.Fatalf("show/quit not delegated: %d/%d", shown, quits)
	}
	local = true
	c.SetQuitting(true)
	c.RefreshMenu()
	c.toggleItem.Click(nil, nil)
	c.quitItem.Click(nil, nil)
	if !c.toggleItem.Disabled || toggles != 1 || quits != 1 {
		t.Fatal("quitting controller still dispatched actions")
	}
	c.Destroy()
}
