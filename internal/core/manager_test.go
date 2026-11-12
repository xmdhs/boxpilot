package core

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testProfile(t *testing.T, root, mode string) Profile {
	t.Helper()
	content := DefaultConfig()
	if mode != "" {
		var cfg map[string]any
		json.Unmarshal([]byte(content), &cfg)
		cfg["_test_mode"] = mode
		b, _ := json.Marshal(cfg)
		content = string(b)
	}
	p, err := SaveProfile(root, "", "test", content)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func cleanupManager(t *testing.T, m *Manager) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.Stop(ctx); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
}
func TestInspectAndCheck(t *testing.T) {
	binary := helperBinary(t)
	installed, err := Inspect(context.Background(), binary)
	if err != nil || installed.Version != "1.14.4" || !filepath.IsAbs(installed.Path) {
		t.Fatalf("inspect: %+v %v", installed, err)
	}
	t.Setenv("BOXPILOT_HELPER_VERSION", "1.13.9")
	if _, err = Inspect(context.Background(), binary); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("old version: %v", err)
	}
	t.Setenv("BOXPILOT_HELPER_VERSION", "1.14.0-alpha.1")
	if _, err = Inspect(context.Background(), binary); err != nil {
		t.Fatalf("prerelease: %v", err)
	}
	if _, err = Inspect(context.Background(), "sing-box"); err == nil {
		t.Fatal("accepted relative executable")
	}
	root := t.TempDir()
	p := testProfile(t, root, "")
	if _, err = Check(context.Background(), binary, p.Path); err != nil {
		t.Fatal(err)
	}
	p = testProfile(t, root, "check-fail")
	out, err := Check(context.Background(), binary, p.Path)
	if err == nil || !strings.Contains(out, "check diagnostic") {
		t.Fatalf("check diagnostic: %q %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Check(ctx, binary, p.Path); !errors.Is(err, context.Canceled) {
		t.Fatalf("check canceled: %v", err)
	}
}
func TestManagerLifecycleContextDetachmentAndRedaction(t *testing.T) {
	binary := helperBinary(t)
	root := t.TempDir()
	p := testProfile(t, root, "")
	var manager *Manager
	var phasesMu sync.Mutex
	var phases []string
	manager = NewManager(root, func(s State) {
		_ = manager.State()
		phasesMu.Lock()
		phases = append(phases, s.Phase)
		phasesMu.Unlock()
	})
	cleanupManager(t, manager)
	ctx, cancel := context.WithCancel(context.Background())
	if err := manager.Start(ctx, binary, p, false); err != nil {
		t.Fatal(err)
	}
	state := manager.State()
	if state.Phase != "running" || state.PID <= 0 || state.Version != "1.14.4" || !strings.HasPrefix(state.URL, "http://127.0.0.1:") || len(state.Secret) < 48 {
		t.Fatalf("wrong running state: %+v", state)
	}
	cancel()
	if err := probeAPI(context.Background(), state.URL, state.Secret); err != nil {
		t.Fatalf("UI context killed running process: %v", err)
	}
	if !manager.Running() {
		t.Fatal("not running")
	}
	waitUntil(t, func() bool { return strings.Contains(manager.State().Output, "helper started") })
	if strings.Contains(manager.State().Output, state.Secret) || !strings.Contains(manager.State().Output, "[REDACTED]") {
		t.Fatal("state output leaked secret")
	}
	configPath := filepath.Join(filepath.Dir(p.Path), "boxpilot-derived-config.json")
	if runtime.GOOS != "windows" {
		runDirs, err := os.ReadDir(filepath.Join(root, "runtime"))
		if err != nil || len(runDirs) != 1 {
			t.Fatalf("runtime dirs: %v %v", runDirs, err)
		}
		configPath = filepath.Join(root, "runtime", runDirs[0].Name(), "config.json")
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("runtime config not private")
	}
	original, err := ReadProfile(p)
	if err != nil || original != DefaultConfig() {
		t.Fatal("original config rewritten")
	}
	if err = manager.Start(context.Background(), binary, p, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate start: %v", err)
	}
	if err = manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopped := manager.State()
	if stopped.Phase != "stopped" || stopped.PID != 0 || stopped.Secret != "" || stopped.URL != "" || manager.Running() {
		t.Fatalf("wrong stopped state: %+v", stopped)
	}
	if strings.Contains(stopped.Output, state.Secret) {
		t.Fatal("stopped output leaked secret")
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(configPath); err != nil {
			t.Fatalf("stable derived config unexpectedly removed: %v", err)
		}
	} else {
		runDirs, err := os.ReadDir(filepath.Join(root, "runtime"))
		if err != nil || len(runDirs) != 0 {
			t.Fatalf("runtime files were persisted after stop: %v %v", runDirs, err)
		}
	}
	if err = manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		phasesMu.Lock()
		defer phasesMu.Unlock()
		return len(phases) > 0 && phases[len(phases)-1] == "stopped"
	})
	if err = manager.Start(context.Background(), binary, p, false); err != nil {
		t.Fatalf("restart: %v", err)
	}
}
func TestManagerFailures(t *testing.T) {
	binary := helperBinary(t)
	for _, mode := range []string{"check-fail", "exit", "no-auth", "not-ready"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			p := testProfile(t, root, mode)
			m := NewManager(root, nil)
			m.startTimeout = 700 * time.Millisecond
			m.stopTimeout = 100 * time.Millisecond
			cleanupManager(t, m)
			err := m.Start(context.Background(), binary, p, false)
			if err == nil {
				t.Fatal("unexpected start success")
			}
			state := m.State()
			if state.Phase != "error" || state.PID != 0 || state.Secret != "" || state.Error == "" {
				t.Fatalf("wrong error state: %+v", state)
			}
			if mode == "exit" && !strings.Contains(state.Output, "early exit diagnostic") {
				t.Fatalf("missing exit output: %+v", state)
			}
			if m.run != nil {
				t.Fatal("failed process not reaped")
			}
		})
	}
}
func TestManagerConflictAndExplicitReplacement(t *testing.T) {
	binary := helperBinary(t)
	root := t.TempDir()
	source := `{"services":[{"type":"api","listen":"0.0.0.0","secret":"user-owned"}]}`
	p, err := SaveProfile(root, "", "conflict", source)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(root, nil)
	cleanupManager(t, m)
	if err = m.Start(context.Background(), binary, p, false); !errors.Is(err, ErrAPIConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if err = m.Start(context.Background(), binary, p, true); err != nil {
		t.Fatal(err)
	}
	original, err := ReadProfile(p)
	if err != nil || original != source {
		t.Fatal("replacement modified source")
	}
}
func TestManagerStopDuringStartAndCanceledCaller(t *testing.T) {
	binary := helperBinary(t)
	for _, mode := range []string{"check-wait", "not-ready"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			p := testProfile(t, root, mode)
			m := NewManager(root, nil)
			cleanupManager(t, m)
			// A blocking probe proves Stop cancels readiness work, not just the process.
			entered := make(chan struct{}, 1)
			if mode == "not-ready" {
				m.probe = func(ctx context.Context, _, _ string) error { entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }
			}
			result := make(chan error, 1)
			go func() { result <- m.Start(context.Background(), binary, p, false) }()
			if mode == "not-ready" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("probe not reached")
				}
			} else {
				waitUntil(t, func() bool {
					pattern := filepath.Join(root, "runtime", "run-*", "config.json.checking")
					if runtime.GOOS == "windows" {
						pattern = filepath.Join(filepath.Dir(p.Path), "boxpilot-derived-config.json.checking")
					}
					matches, _ := filepath.Glob(pattern)
					return len(matches) == 1
				})
			}
			if err := m.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := <-result; err == nil {
				t.Fatal("start not canceled")
			}
			if m.State().Phase != "stopped" {
				t.Fatalf("state: %+v", m.State())
			}
		})
	}
	root := t.TempDir()
	p := testProfile(t, root, "not-ready")
	m := NewManager(root, nil)
	cleanupManager(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- m.Start(ctx, binary, p, false) }()
	waitUntil(t, func() bool { return m.State().Phase == "starting" })
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation: %v", err)
	}
	if m.State().PID != 0 {
		t.Fatal("canceled startup left process")
	}
}
func TestManagerCallbackMayStop(t *testing.T) {
	binary := helperBinary(t)
	root := t.TempDir()
	p := testProfile(t, root, "")
	done := make(chan error, 1)
	var m *Manager
	m = NewManager(root, func(s State) {
		if s.Phase == "running" {
			done <- m.Stop(context.Background())
		}
	})
	cleanupManager(t, m)
	if err := m.Start(context.Background(), binary, p, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback deadlocked Stop")
	}
}
func TestManagerCrashAndForcedStop(t *testing.T) {
	binary := helperBinary(t)
	root := t.TempDir()
	m := NewManager(root, nil)
	cleanupManager(t, m)
	if err := m.Start(context.Background(), binary, testProfile(t, root, ""), false); err != nil {
		t.Fatal(err)
	}
	state := m.State()
	client := &http.Client{Timeout: time.Second}
	resp, _ := client.Get(state.URL + "/crash")
	if resp != nil {
		resp.Body.Close()
	}
	waitUntil(t, func() bool { return m.State().Phase == "error" })
	if m.State().PID != 0 || m.State().Secret != "" {
		t.Fatal("crash did not clear process")
	}
	root = t.TempDir()
	m = NewManager(root, nil)
	m.stopTimeout = 50 * time.Millisecond
	cleanupManager(t, m)
	if err := m.Start(context.Background(), binary, testProfile(t, root, "ignore-stop"), false); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Stop(canceled)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return m.State().Phase == "stopped" })
}
func TestStopDoesNotTouchExternalProcess(t *testing.T) {
	binary := helperBinary(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	derived, err := deriveConfig([]byte(DefaultConfig()), port, "external-secret", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "external.json")
	if err = os.WriteFile(cfg, derived, 0600); err != nil {
		t.Fatal(err)
	}
	external := exec.Command(binary, "run", "-c", cfg)
	if err = external.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { external.Process.Kill(); external.Wait() })
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	waitUntil(t, func() bool { return probeAPI(context.Background(), endpoint, "external-secret") == nil })
	root := t.TempDir()
	m := NewManager(root, nil)
	cleanupManager(t, m)
	if err = m.Start(context.Background(), binary, testProfile(t, root, ""), false); err != nil {
		t.Fatal(err)
	}
	if err = m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = probeAPI(context.Background(), endpoint, "external-secret"); err != nil {
		t.Fatalf("external instance affected: %v", err)
	}
}
func TestBoundedOutputAndSecretEdges(t *testing.T) {
	var b boundedOutput
	b.Write([]byte(strings.Repeat("x", maxOutputSize*2)))
	if len(b.String()) != maxOutputSize {
		t.Fatal("output unbounded")
	}
	secret := "0123456789abcdef0123456789abcdef"
	for _, text := range []string{"token=" + secret, "Authorization: Bearer other-secret", secret[:18], secret[10:]} {
		result := redact(text, secret)
		if !strings.Contains(result, "[REDACTED]") || strings.Contains(result, secret) {
			t.Fatalf("not redacted %q => %q", text, result)
		}
	}
}
