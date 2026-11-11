package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/xmdhs/boxpilot/internal/core"
	"github.com/xmdhs/boxpilot/internal/storage"
)

func TestCoreReadyCannotReplaceNewerRemoteSelection(t *testing.T) {
	a := fixture(t)
	a.settings.ActiveID = "remote-b"
	a.generation = 8
	client := a.client
	a.coreStarted(core.State{Phase: "running", URL: "http://127.0.0.1:9090", Secret: "test-secret"}, 7)
	if a.client != client || a.settings.ActiveID != "remote-b" {
		t.Fatal("old core startup replaced the selected remote client")
	}
	a.settings.ActiveID = localID
	a.coreStarted(core.State{Phase: "running", URL: "http://127.0.0.1:9090"}, 7)
	if a.client != client {
		t.Fatal("stale startup generation was accepted")
	}
}
func TestStartingWithoutInstalledCoreExplainsWhatToDo(t *testing.T) {
	a := fixture(t)
	profile, err := core.SaveProfile(a.root, "", "test", core.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	a.profiles = []core.Profile{profile}
	a.settings.ProfileID = profile.ID
	a.settings.CorePath = ""
	a.manager = core.NewManager(a.root, nil)
	a.startCore(false)
	if !a.errorOpen || !strings.Contains(a.errorText, a.t("核心未安装", "Core is not installed")) {
		t.Fatalf("missing clear not-installed error: open=%t text=%q", a.errorOpen, a.errorText)
	}
}

func TestCoreStartDisconnectsOldRemoteImmediately(t *testing.T) {
	a := fixture(t)
	a.settings.ActiveID = "remote-a"
	p, err := core.SaveProfile(a.root, "", "test", core.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	a.profiles = []core.Profile{p}
	a.settings.ProfileID = p.ID
	a.settings.CorePath = "/nonexistent/boxpilot-test-sing-box"
	a.manager = core.NewManager(a.root, nil)
	a.coreState = core.State{Phase: "stopped"}
	// The fixture drops main-thread callbacks because this test does not run a UI loop.
	a.startCore(false)
	if a.settings.ActiveID != localID || a.client != nil {
		t.Fatal("local selection still points to remote client")
	}
}
func TestCredentialJobsAreNotDroppedOnQuickSwitch(t *testing.T) {
	a := fixture(t)
	a.client = nil
	a.settings.Servers = []storage.Server{{ID: "server-a", Name: "A", URL: "http://127.0.0.1:9091", Remember: "file"}, {ID: "server-b", Name: "B", URL: "http://127.0.0.1:9092", Remember: "file"}}
	for _, s := range a.settings.Servers {
		if err := storage.SaveSecret(a.root, s.ID, "test-only", s.Remember); err != nil {
			t.Fatal(err)
		}
	}
	a.activate("server-a")
	a.activate("server-b")
	jobs := 0
	for k := range a.busy {
		if strings.HasPrefix(k, "credentials-") {
			jobs++
		}
	}
	if jobs != 2 {
		t.Fatalf("queued credential jobs=%d, want 2", jobs)
	}
}
func TestDeleteServerUsesSavedSecretMode(t *testing.T) {
	a := fixture(t)
	a.client = nil
	a.settings.Servers = []storage.Server{{ID: "server-a", Name: "A", Remember: "file"}}
	if err := storage.SaveSecret(a.root, "server-a", "stored-plaintext-test", "file"); err != nil {
		t.Fatal(err)
	}
	a.serverID = "server-a"
	a.serverRemember = "session"
	a.deleteServer()
	a.workers.Wait()
	if _, err := storage.LoadSecret(a.root, "server-a", "file"); !errors.Is(err, storage.ErrSecretNotFound) {
		t.Fatalf("old file credential remains: %v", err)
	}
}
func TestCredentialLoadDisablesSecretEditing(t *testing.T) {
	a := fixture(t)
	a.serverDialog = true
	a.serverLoading = true
	a.serverSecret = "unchanged"
	tt := ui.NewTester(a.view, 1140, 780)
	access, ok := tt.AccessByRole(ui.RoleTextField, "Secret")
	if !ok || !access.Disabled {
		t.Fatal("secret field remains editable while old credentials load")
	}
	if err := tt.Click("Secret"); err == nil {
		tt.Type("new value")
	}
	if a.serverSecret != "unchanged" {
		t.Fatal("disabled secret field changed")
	}
}
func TestSavingProfileLocksEditorAndCannotDismiss(t *testing.T) {
	a := fixture(t)
	a.profileEditor = true
	a.profileName = "Config"
	a.profileContent = core.DefaultConfig()
	a.busy["save-profile"] = true
	tt := ui.NewTester(a.view, 1140, 780)
	for _, label := range []string{"Name", "Configuration"} {
		access, ok := tt.Access(label)
		if !ok || !access.Disabled {
			t.Fatalf("%s not locked during save", label)
		}
	}
	tt.Key(0, ui.KeyEscape)
	if !a.profileEditor {
		t.Fatal("saving editor dismissed via Escape")
	}
	a.closeProfile()
	if !a.profileEditor {
		t.Fatal("saving editor dismissed via close action")
	}
}
func TestUnsavedConfigurationConfirmationOnTop(t *testing.T) {
	a := fixture(t)
	a.profileEditor = true
	a.profileDirty = true
	a.profileName = "Config"
	a.profileContent = core.DefaultConfig()
	tt := ui.NewTester(a.view, 1140, 780)
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if !a.confirmOpen {
		t.Fatal("missing discard confirmation")
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatal(err)
	}
	if a.profileEditor || a.profileDirty {
		t.Fatal("discard did not close editor")
	}
}
