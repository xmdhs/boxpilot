package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPageMotionRespectsReducedMotionAndSettles(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		name := "normal"
		if reduced {
			name = "reduced"
		}
		t.Run(name, func(t *testing.T) {
			page := "first"
			tt := ui.NewTesterWith(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() {
					ui.Box(c.Key(page)).Size(100, 80).Label("page").Background(ui.Hex("#007aff")).Transition(pageTransition)
				})
			}, 240, 180, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})
			rest, ok := tt.Find("page")
			if !ok {
				t.Fatal("页面不存在")
			}
			page = "second"
			tt.Frame()
			start, ok := tt.Find("page")
			if !ok || start.W != rest.W || start.H != rest.H {
				t.Fatalf("入场动画改变了页面尺寸：rest=%+v start=%+v", rest, start)
			}
			if reduced && start != rest {
				t.Fatalf("减少动态效果时页面仍有位移：rest=%+v start=%+v", rest, start)
			}
			if !reduced && start.Y <= rest.Y {
				t.Fatalf("页面未从下方入场：rest=%+v start=%+v", rest, start)
			}
			tt.Advance(300 * time.Millisecond)
			end, ok := tt.Find("page")
			if !ok || end != rest {
				t.Fatalf("动画结束后页面未恢复布局位置：rest=%+v end=%+v", rest, end)
			}
			if tt.FramePending() {
				t.Fatal("动画结束后仍在请求刷新")
			}
		})
	}
}

func TestInteractionMotionOnlyAnimatesColors(t *testing.T) {
	selected := false
	tt := ui.NewTester(func(c *ui.Context) {
		color, width := ui.Hex("#007aff"), float32(80)
		if selected {
			color, width = ui.Hex("#62ba46"), 100
		}
		ui.Box(c).Size(width, 40).Label("control").Background(color).Transition(interactionTransition)
	}, 160, 80)
	before := tt.Image().RGBAAt(10, 10)
	selected = true
	tt.Frame()
	bounds, ok := tt.Find("control")
	if !ok || bounds.W != 100 {
		t.Fatalf("颜色过渡不应延迟尺寸更新：%+v", bounds)
	}
	tt.Advance(60 * time.Millisecond)
	middle := tt.Image().RGBAAt(10, 10)
	tt.Advance(120 * time.Millisecond)
	after := tt.Image().RGBAAt(10, 10)
	if middle == before || middle == after || after.R != 0x62 || after.G != 0xba || after.B != 0x46 {
		t.Fatalf("交互颜色未平滑过渡：before=%v middle=%v after=%v", before, middle, after)
	}
	if tt.FramePending() {
		t.Fatal("颜色过渡结束后仍在请求刷新")
	}
}

func TestAnimatedOverlaysRemainDismissible(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, overlay := range []string{"navigation", "connection", "dialog"} {
			name := "normal/" + overlay
			if reduced {
				name = "reduced/" + overlay
			}
			t.Run(name, func(t *testing.T) {
				a := fixture(t)
				width := 1140
				if overlay == "navigation" {
					width = 600
				}
				tt := ui.NewTesterWith(a.view, width, 780, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})
				open := func() {
					switch overlay {
					case "navigation":
						a.compactMenu = true
					case "connection":
						a.page = "connections"
						a.detailID = "connection-1"
					case "dialog":
						a.errorOpen = true
						a.errorText = "Test error"
					}
					tt.Frame()
				}
				closed := func() bool {
					switch overlay {
					case "navigation":
						return !a.compactMenu
					case "connection":
						return a.detailID == ""
					default:
						return !a.errorOpen
					}
				}
				// 入场尚未结束就关闭，再立即打开，确保旧动画不影响新一轮交互。
				for range 2 {
					open()
					tt.Advance(50 * time.Millisecond)
					tt.Key(0, ui.KeyEscape)
					if !closed() {
						t.Fatal("动画期间 Escape 未能关闭浮层")
					}
				}
				tt.Advance(300 * time.Millisecond)
				if _, ok := tt.AccessByRole(ui.RoleDialog, ""); ok {
					t.Fatal("浮层关闭后仍在无障碍树中")
				}
				a.page = "settings"
				tt.Frame()
				if err := tt.Click("New Server"); err != nil || !a.serverDialog {
					t.Fatalf("退出动画阻挡了设置页操作：err=%v dialog=%t", err, a.serverDialog)
				}
			})
		}
	}
}
