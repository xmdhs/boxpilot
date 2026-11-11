//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInstallLockRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := acquireInstallLock(path); err == nil {
		f.Close()
		t.Fatal("accepted FIFO lock")
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("replaced rejected FIFO: %v, %v", info, err)
	}
	// Test the platform open directly as well: O_NONBLOCK prevents a FIFO
	// swapped in after the common Lstat check from hanging the installer.
	if f, err := openInstallLock(path); err == nil {
		f.Close()
		t.Fatal("platform open accepted FIFO lock")
	}
}
