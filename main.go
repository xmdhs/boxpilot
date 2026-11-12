package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/xmdhs/boxpilot/internal/app"
	"github.com/xmdhs/boxpilot/internal/core"
	"github.com/xmdhs/boxpilot/internal/storage"
)

func main() {
	if handled, exitCode, err := core.RunElevatedCoreAction(os.Args[1:]); handled {
		if err != nil {
			log.Printf("run elevated process action: %v", err)
			os.Exit(1)
		}
		os.Exit(exitCode)
	}
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
	if err := mygo.App.RegisterURLScheme("sing-box"); err != nil {
		log.Printf("register sing-box URL scheme: %v", err)
	}
	root, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	settings, loadErr := storage.Load(root)
	a := app.New(root, settings)
	mygo.App.WhenReady(func() { a.Open(loadErr) })
	mygo.App.OnOpenURL(a.OpenURL)
	mygo.App.OnSecondInstance(func([]string, string) { a.Show() })
	mygo.App.OnWindowAllClosed(a.WindowAllClosed)
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
