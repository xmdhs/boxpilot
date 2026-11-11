package app

import (
	"sync"
	"testing"
	"time"

	"github.com/xmdhs/boxpilot/internal/storage"
)

func TestCredentialMigrationStaysBusyUntilCommit(t *testing.T) {
	a := fixture(t)
	a.client = nil
	a.settings.Servers = []storage.Server{{ID: "server", Name: "server", URL: "http://127.0.0.1:9", Remember: storage.RememberSession}}
	if err := storage.Save(a.root, a.settings); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveSecret(a.root, "server", "old", storage.RememberSession); err != nil {
		t.Fatal(err)
	}
	callbacks := make(chan func(), 16)
	a.dispatchMain = func(fn func()) { callbacks <- fn }
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	a.queueSaveCommitted(a.settings, func() error { close(entered); <-release; return nil })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("save queue did not reach barrier")
	}
	a.serverID, a.serverName, a.serverURL = "server", "server", "http://127.0.0.1:9"
	a.serverRemember, a.serverFileConsent, a.serverSecret = storage.RememberFile, true, "first"
	a.saveServer()
	var callback func()
	select {
	case callback = <-callbacks:
	case <-time.After(5 * time.Second):
		t.Fatal("credential worker did not finish")
	}
	a.workers.Wait()
	a.cancel()
	callback()
	if !a.busy["save-server"] {
		t.Fatal("migration unlocked before settings committed")
	}
	// 若允许第二次迁移，第一次清理旧存储可能误删第二次刚写入的密钥。
	a.serverRemember, a.serverSecret = storage.RememberSession, "second"
	a.saveServer()
	a.workers.Wait()
	if value, err := storage.LoadSecret(a.root, "server", storage.RememberFile); err != nil || value != "first" {
		t.Fatalf("overlapping migration changed credentials: %q %v", value, err)
	}
	unblock()
	a.flushSaves()
	for {
		select {
		case fn := <-callbacks:
			fn()
		default:
			if a.busy["save-server"] {
				t.Fatal("committed migration remained locked")
			}
			return
		}
	}
}
