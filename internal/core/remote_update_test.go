package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func remoteUpdateFixture(t *testing.T) (string, Profile) {
	t.Helper()
	root := t.TempDir()
	p, err := SaveProfileWithMetadata(root, Profile{Type: "remote", RemoteURL: "https://a.invalid/config", AutoUpdate: true, AutoUpdateInterval: 60}, "original", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return root, p
}

func TestRemoteUpdateRejectsStaleMetadata(t *testing.T) {
	root, original := remoteUpdateFixture(t)
	update, err := PrepareRemoteProfileUpdate(root, original, `{ "outbounds": [] }`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.RemoteURL, changed.AutoUpdate = "https://b.invalid/config", false
	current, err := SaveProfileWithMetadata(root, changed, "new name", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = update.Commit(); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("stale commit: %v", err)
	}
	if _, _, err = UpdateRemoteProfile(root, original, `{}`, time.Now()); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("stale download: %v", err)
	}
	profiles, err := Profiles(root)
	if err != nil || len(profiles) != 1 || !sameRemoteProfile(profiles[0], current) {
		t.Fatalf("new metadata overwritten: %+v %v", profiles, err)
	}
	if content, _ := ReadProfile(current); content != `{}` {
		t.Fatal("new content overwritten")
	}
}

func TestRemoteUpdateRejectsConcurrentContentAndDeletion(t *testing.T) {
	for _, action := range []string{"content", "delete"} {
		t.Run(action, func(t *testing.T) {
			root, original := remoteUpdateFixture(t)
			update, err := PrepareRemoteProfileUpdate(root, original, `{ "outbounds": [] }`, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if action == "delete" {
				err = DeleteProfile(root, original.ID)
			} else {
				_, err = SaveProfileWithMetadata(root, original, original.Name, `{}`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = update.Commit(); !errors.Is(err, ErrProfileChanged) {
				t.Fatalf("concurrent change accepted: %v", err)
			}
		})
	}
}

type updateTestController struct {
	stops       int
	contents    []string
	failStart   bool
	onStart     func()
	cancel      context.CancelFunc
	apiReplaces []bool
	cpuChoices  [][]uint32
}

func (m *updateTestController) Start(ctx context.Context, binary string, p Profile, replace bool, cpu ...[]uint32) error {
	content, err := ReadProfile(p)
	if err != nil {
		return err
	}
	m.contents = append(m.contents, content)
	m.apiReplaces = append(m.apiReplaces, replace)
	if len(cpu) > 0 {
		m.cpuChoices = append(m.cpuChoices, append([]uint32(nil), cpu[0]...))
	}
	if len(m.contents) == 1 {
		if m.onStart != nil {
			m.onStart()
		}
		if m.cancel != nil {
			m.cancel()
			return ctx.Err()
		}
		if m.failStart {
			return errors.New("candidate startup failed")
		}
	}
	return nil
}
func (m *updateTestController) Stop(context.Context) error                     { m.stops++; return nil }
func (m *updateTestController) State() State                                   { return State{Phase: "running"} }
func (m *updateTestController) SetCPUAffinity(context.Context, []uint32) error { return nil }

func TestRemoteUpdateInvalidCandidateKeepsRunningCore(t *testing.T) {
	binary := helperBinary(t)
	root, original := remoteUpdateFixture(t)
	update, err := PrepareRemoteProfileUpdate(root, original, `{"_test_mode":"check-fail"}`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	manager := &updateTestController{}
	if _, err = update.Restart(context.Background(), manager, binary, false, nil); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	if manager.stops != 0 || len(manager.contents) != 0 {
		t.Fatal("validation failure interrupted the running core")
	}
	if content, _ := ReadProfile(original); content != DefaultConfig() {
		t.Fatal("invalid candidate overwritten original")
	}
}

func TestRemoteUpdateRestartCommitAndRecovery(t *testing.T) {
	for _, mode := range []string{"success", "start-failure", "concurrent-edit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			binary := helperBinary(t)
			root, original := remoteUpdateFixture(t)
			candidate := `{"outbounds":[{"type":"direct","tag":"new"}]}`
			update, err := PrepareRemoteProfileUpdate(root, original, candidate, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager := &updateTestController{failStart: mode == "start-failure"}
			if mode == "cancel" {
				manager.cancel = cancel
			}
			if mode == "concurrent-edit" {
				manager.onStart = func() {
					changed := original
					changed.RemoteURL = "https://new.invalid/config"
					if _, err := SaveProfileWithMetadata(root, changed, "new name", `{}`); err != nil {
						t.Fatal(err)
					}
				}
			}
			updated, err := update.Restart(ctx, manager, binary, true, []uint32{2, 4})
			if mode == "success" {
				if err != nil || updated.LastUpdated.IsZero() {
					t.Fatalf("restart: %+v %v", updated, err)
				}
				if content, _ := ReadProfile(original); content != candidate {
					t.Fatal("successful candidate not committed")
				}
			} else {
				if err == nil {
					t.Fatal("expected update failure")
				}
				wantContent := DefaultConfig()
				if mode == "concurrent-edit" {
					wantContent = `{}`
					if !errors.Is(err, ErrProfileChanged) {
						t.Fatal(err)
					}
				}
				if content, _ := ReadProfile(original); content != wantContent {
					t.Fatal("failed update changed saved content")
				}
				if mode != "cancel" && (len(manager.contents) != 2 || manager.contents[1] != DefaultConfig() || !strings.Contains(err.Error(), "restored")) {
					t.Fatalf("old running snapshot not restored: %+v %v", manager.contents, err)
				}
				if mode == "cancel" && (len(manager.contents) != 1 || !errors.Is(err, context.Canceled)) {
					t.Fatal("canceled update restarted the core")
				}
			}
			for i := range manager.apiReplaces {
				if !manager.apiReplaces[i] || !reflect.DeepEqual(manager.cpuChoices[i], []uint32{2, 4}) {
					t.Fatal("restart lost approval or CPU preference")
				}
			}
		})
	}
}
