package main

import (
	"log"
	"os"

	"boxpilot/internal/app"
	"boxpilot/internal/storage"
	"github.com/egoist/mygo"
)

func main() {
	if mygo.IsDev() {
		mygo.App.SetName("BoxPilot Dev")
	} else {
		mygo.App.SetName("BoxPilot")
	}
	// CLI 的生成阶段不应创建用户数据目录或初始化桌面后端。
	if os.Getenv("MYGO_GENERATE") != "" {
		if err := mygo.App.Run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	root, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	settings, loadErr := storage.Load(root)
	a := app.New(root, settings)
	mygo.App.WhenReady(func() { a.Open(loadErr) })
	mygo.App.OnSecondInstance(func([]string, string) { a.Show() })
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			a.Show()
		}
	})
	mygo.App.OnBeforeQuit(a.BeforeQuit)
	mygo.App.OnQuitCanceled(a.QuitCanceled)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.Destroy() })
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
