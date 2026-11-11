package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInstallLockLegacyFileAndRelease(t *testing.T) {
	for _, content := range []string{"", "legacy marker\n"} {
		t.Run(fmt.Sprintf("bytes-%d", len(content)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".1.14.4.lock")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := acquireInstallLock(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lock.Close() })
			var wg sync.WaitGroup
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if other, err := acquireInstallLock(path); err == nil {
						other.Close()
						t.Error("concurrent installer acquired active lock")
					}
				}()
			}
			wg.Wait()
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				retry, err := acquireInstallLock(path)
				if err != nil {
					t.Fatalf("retry %d: %v", i, err)
				}
				if err := retry.Close(); err != nil {
					t.Fatal(err)
				}
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("lock file removed or replaced: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatalf("legacy contents modified: %q, %v", data, err)
			}
		})
	}
}

func TestInstallLockRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling-symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "lock")
			target := filepath.Join(dir, "target")
			if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink", "dangling-symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege dependent; no elevation in tests")
				}
				if kind == "dangling-symlink" {
					target = filepath.Join(dir, "missing")
				}
				err = os.Symlink(target, path)
			case "hardlink":
				err = os.Link(target, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if f, err := acquireInstallLock(path); err == nil {
				f.Close()
				t.Fatal("accepted unsafe lock")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("removed rejected entry: %v", err)
			}
			if kind == "dangling-symlink" {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("created symlink target: %v", err)
				}
			} else if data, err := os.ReadFile(target); err != nil || string(data) != "unchanged" {
				t.Fatalf("modified target: %q, %v", data, err)
			}
		})
	}
}

// This subprocess runs only this test executable, never a downloaded core.
func TestInstallLockProcessHelper(t *testing.T) {
	path := os.Getenv("BOXPILOT_TEST_INSTALL_LOCK_PATH")
	if path == "" {
		return
	}
	lock, err := acquireInstallLock(path)
	if os.Getenv("BOXPILOT_TEST_INSTALL_LOCK_MODE") == "busy" {
		if err == nil {
			lock.Close()
			t.Fatal("subprocess acquired parent's active lock")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
	runtime.KeepAlive(lock)
	// Deliberately exit without closing the file to verify OS cleanup.
	os.Exit(0)
}

func lockHelperCommand(t *testing.T, path, mode string) *exec.Cmd {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestInstallLockProcessHelper$")
	cmd.Env = append(os.Environ(), "BOXPILOT_TEST_INSTALL_LOCK_PATH="+path, "BOXPILOT_TEST_INSTALL_LOCK_MODE="+mode)
	return cmd
}

func TestInstallLockCrossProcessAndExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	parent, err := acquireInstallLock(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Close() })
	if out, err := lockHelperCommand(t, path, "busy").CombinedOutput(); err != nil {
		t.Fatalf("cross-process exclusion: %v: %s", err, out)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"exit", "killed"} {
		t.Run(mode, func(t *testing.T) {
			cmd := lockHelperCommand(t, path, mode)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			// CommandContext bounds this pipe read even if the child fails.
			line, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "locked" {
				t.Fatalf("child did not acquire lock: %q, %v", line, err)
			}
			for i := 0; i < 3; i++ {
				if f, err := acquireInstallLock(path); err == nil {
					f.Close()
					t.Fatal("acquired or removed child's active lock")
				}
			}
			if mode == "killed" {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			if mode == "exit" && err != nil {
				t.Fatalf("child exit: %v", err)
			}
			retry, err := acquireInstallLock(path)
			if err != nil {
				t.Fatalf("lock not released after %s: %v", mode, err)
			}
			if err := retry.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallLegacyLockConcurrencyAndFailureRetry(t *testing.T) {
	root := t.TempDir()
	versions := filepath.Join(root, "versions")
	if err := os.Mkdir(versions, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(versions, ".1.14.4.lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	r := fixtureRelease(t, []byte("fixture, never downloaded or executed"))
	entered := make(chan struct{}, 2)
	unblock := make(chan struct{}, 1)
	defer close(unblock)
	failedDownload := errors.New("simulated download failure")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		select {
		case <-unblock:
			return nil, failedDownload
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})}
	first := make(chan error, 1)
	go func() {
		_, err := install(ctx, root, r, nil, client)
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("legacy lock prevented download attempt")
	}
	if _, err := install(ctx, root, r, nil, client); err == nil || !strings.Contains(err.Error(), "install lock") {
		t.Fatalf("concurrent install not rejected at lock: %v", err)
	}
	if f, err := acquireInstallLock(path); err == nil {
		f.Close()
		t.Fatal("failed contender removed active install lock")
	}
	unblock <- struct{}{}
	if err := <-first; !errors.Is(err, failedDownload) {
		t.Fatalf("first install: %v", err)
	}
	unblock <- struct{}{}
	if _, err := install(ctx, root, r, nil, client); !errors.Is(err, failedDownload) {
		t.Fatalf("retry did not reach download: %v", err)
	}
	select {
	case <-entered:
	default:
		t.Fatal("retry did not attempt download")
	}
	assertNoStaging(t, root)
}
