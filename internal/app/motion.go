package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// 仅对交互状态做颜色过渡，避免悬停或数据刷新带动布局。
var interactionTransition = ui.ElementTransition{
	Duration: 120 * time.Millisecond,
	Colors:   true,
}

// 页面只播放入场动画，切换时不保留旧页面，也不延迟新页面的操作。
var pageTransition = ui.ElementTransition{
	Duration: 250 * time.Millisecond,
	Colors:   true,
	Enter:    &ui.Motion{Y: 4},
}

var overlayTransition = ui.ElementTransition{
	Duration:     180 * time.Millisecond,
	ExitDuration: 120 * time.Millisecond,
	Colors:       true,
	Enter:        &ui.Motion{},
	Exit:         &ui.Motion{},
}

// MyGo 的 Transition 会跟随系统 ReduceMotion 设置，将位移和缩放降级为原地淡入淡出。
func animateDialog(back, panel ui.Element) {
	back.Transition(overlayTransition)
	panel.Transition(ui.ElementTransition{
		Duration: 180 * time.Millisecond,
		Colors:   true,
		Enter:    &ui.Motion{Scale: .97},
	})
}

func drawerTransition(x float32) ui.ElementTransition {
	return ui.ElementTransition{
		Duration: 180 * time.Millisecond,
		Colors:   true,
		Enter:    &ui.Motion{X: x},
	}
}

func navigationDrawerTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: 200 * time.Millisecond,
		Colors:   true,
		Enter:    &ui.Motion{XPercent: -100, Opacity: 1},
	}
}
