package app

import "github.com/egoist/mygo/ui"

const (
	sidebarWidth      float32 = 232
	compactBreakpoint float32 = 720
	pagePadding       float32 = 24
	pageGap           float32 = 18
	actionBreakpoint  float32 = 600
	overviewCardWidth float32 = 300
	groupItemWidth    float32 = 180
)

// 页面操作区统一按壳层分配的内容宽度决定是否堆叠，网格则交给弹性布局实际换行。
func pageContentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	if width > compactBreakpoint {
		width -= sidebarWidth
	}
	return max(0, width-2*pagePadding)
}

func narrowPage(c *ui.Context) bool { return pageContentWidth(c) < actionBreakpoint }
