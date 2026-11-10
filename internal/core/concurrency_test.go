package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestConcurrentStartsAndStops(t *testing.T) {
	binary := helperBinary(t)
	root := t.TempDir()
	p := testProfile(t, root, "")
	m := NewManager(root, nil)
	cleanupManager(t, m)
	entered := make(chan struct{})
	m.probe = func(ctx context.Context, _, _ string) error { close(entered); <-ctx.Done(); return ctx.Err() }
	first := make(chan error, 1)
	go func() { first <- m.Start(context.Background(), binary, p, false) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not reach probe")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Start(context.Background(), binary, p, false); !errors.Is(err, ErrBusy) {
				t.Errorf("concurrent start: %v", err)
			}
			_ = m.State()
			_ = m.Running()
		}()
	}
	wg.Wait()
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Stop(context.Background()); err != nil {
				t.Errorf("concurrent stop: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Start did not cancel: %v", err)
	}
	if state := m.State(); state.Phase != "stopped" || state.PID != 0 {
		t.Fatalf("state: %+v", state)
	}
}
func TestCallbackMayCancelChecking(t *testing.T) {
	binary := helperBinary(t)
	root := t.TempDir()
	p := testProfile(t, root, "")
	stopped := make(chan error, 1)
	var m *Manager
	m = NewManager(root, func(s State) {
		if s.Phase == "checking" {
			stopped <- m.Stop(context.Background())
		}
	})
	cleanupManager(t, m)
	if err := m.Start(context.Background(), binary, p, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation: %v", err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checking callback deadlocked")
	}
}
