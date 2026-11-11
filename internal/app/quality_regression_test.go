package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xmdhs/boxpilot/internal/storage"
)

func TestPostDoesNotReadWindowOnWorker(t *testing.T) {
	const count = 10000
	callbacks := make(chan func(), count)
	a := &App{dispatchMain: func(fn func()) { callbacks <- fn }}
	start, done := make(chan struct{}), make(chan struct{})
	applied := 0
	go func() {
		defer close(done)
		<-start
		for i := 0; i < count; i++ {
			a.post(func() { applied++ })
		}
	}()
	close(start)
	for i := 0; i < count; i++ {
		// 模拟主线程销毁/替换窗口时的字段写入；后台只允许投递。
		a.win = nil
	}
	<-done
	for i := 0; i < count; i++ {
		(<-callbacks)()
	}
	if applied != count {
		t.Fatalf("callbacks applied=%d", applied)
	}
	a.post(func() { applied++ })
	a.closing = true
	(<-callbacks)()
	if applied != count {
		t.Fatal("closing app applied a queued callback")
	}
}

func TestImportedProfileResetsRemoteEditor(t *testing.T) {
	a := fixture(t)
	a.newRemoteProfile("remote", "https://old.invalid/config")
	a.profileLastUpdated = time.Now()
	a.openImportedProfile(`{"outbounds":[]}`)
	if a.profileType != "local" || a.profileID != "" || a.profileRemoteURL != "" || a.profileAutoUpdate || a.profileAutoUpdateInterval != "" || !a.profileLastUpdated.IsZero() {
		t.Fatal("local import retained remote metadata")
	}
	if !a.profileEditor || !a.profileDirty || a.profileContent != `{"outbounds":[]}` {
		t.Fatal("imported content was not opened")
	}
}

func TestCredentialMigrationCommitAndClosing(t *testing.T) {
	for _, mode := range []string{"success", "closing", "save-failure"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			s := storage.Defaults()
			s.Servers = []storage.Server{{ID: "test-server", Name: "test", URL: "http://127.0.0.1:9", Remember: storage.RememberSession}}
			if err := storage.Save(root, s); err != nil {
				t.Fatal(err)
			}
			if err := storage.SaveSecret(root, "test-server", "old-secret", storage.RememberSession); err != nil {
				t.Fatal(err)
			}
			a := New(root, s)
			callbacks := make(chan func(), 16)
			a.dispatchMain = func(fn func()) { callbacks <- fn }
			t.Cleanup(func() { a.cancel(); a.workers.Wait(); a.flushSaves() })
			a.serverID, a.serverName, a.serverURL = "test-server", "test", "http://127.0.0.1:9"
			a.serverSecret, a.serverRemember, a.serverFileConsent = "new-secret", storage.RememberFile, true
			a.saveServer()
			var callback func()
			select {
			case callback = <-callbacks:
			case <-time.After(5 * time.Second):
				t.Fatal("credential worker did not finish")
			}
			a.workers.Wait()
			if value, err := storage.LoadSecret(root, "test-server", storage.RememberSession); err != nil || value != "old-secret" {
				t.Fatalf("old credential deleted before settings commit: %q %v", value, err)
			}
			if mode == "closing" {
				a.closing = true
			}
			if mode == "save-failure" {
				path := filepath.Join(root, storage.SettingsFile)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			// 禁止连接测试服务器，事务落盘本身不依赖应用 context。
			a.cancel()
			callback()
			a.workers.Wait()
			a.flushSaves()
			_, oldErr := storage.LoadSecret(root, "test-server", storage.RememberSession)
			if mode == "success" {
				if !errors.Is(oldErr, storage.ErrSecretNotFound) {
					t.Fatalf("old credential not removed after commit: %v", oldErr)
				}
				persisted, err := storage.Load(root)
				if err != nil || persisted.Servers[0].Remember != storage.RememberFile {
					t.Fatalf("settings not committed: %+v %v", persisted, err)
				}
				if value, err := storage.LoadSecret(root, "test-server", storage.RememberFile); err != nil || value != "new-secret" {
					t.Fatalf("new credential missing: %q %v", value, err)
				}
			} else if oldErr != nil {
				t.Fatalf("failed/uncommitted migration deleted old credential: %v", oldErr)
			}
			if mode == "closing" {
				persisted, err := storage.Load(root)
				if err != nil || persisted.Servers[0].Remember != storage.RememberSession {
					t.Fatal("closing migration changed saved mode")
				}
			}
		})
	}
}

func TestSettingsCommitHookIsAnOrderedBarrier(t *testing.T) {
	a := fixture(t)
	s := storage.Defaults()
	s.Accent = "first"
	committed := make(chan string, 1)
	a.queueSaveCommitted(s, func() error {
		persisted, err := storage.Load(a.root)
		if err != nil {
			return err
		}
		committed <- persisted.Accent
		return nil
	})
	s.Accent = "last"
	a.queueSave(s)
	a.flushSaves()
	if got := <-committed; got != "first" {
		t.Fatalf("transaction was coalesced or committed out of order: %q", got)
	}
	persisted, err := storage.Load(a.root)
	if err != nil || persisted.Accent != "last" {
		t.Fatalf("latest settings lost: %+v %v", persisted, err)
	}
}
