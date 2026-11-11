//go:build windows

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// These tests only exercise kernel events/owner handles and rejected helper
// invocations. They never invoke ShellExecuteExW, UAC, a GUI, or a real core.
func cancellationTestEvent(t *testing.T) (*elevatedCancelEvent, string) {
	t.Helper()
	token, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	name := `Local\BoxPilot-test-` + token
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateEvent(nil, 1, 0, namePtr)
	if err != nil {
		t.Fatal(err)
	}
	event := &elevatedCancelEvent{handle: handle}
	t.Cleanup(event.close)
	return event, name
}

func cancellationTestOwner(t *testing.T) string {
	t.Helper()
	creation, err := processCreationTime(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(os.Getpid()) + ":" + strconv.FormatUint(creation, 10)
}

func TestElevatedCancellationEventAndOwner(t *testing.T) {
	event, _ := cancellationTestEvent(t)
	owner, err := openHelperOwner(cancellationTestOwner(t))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(owner)
	if !helperMayRun(owner, event.handle) {
		t.Fatal("live owner and unsignaled event must allow startup")
	}
	proc := &elevatedWindowsProcess{cancelEvent: event}
	// CPU scheduling requests may be waiting for a response. Kill must not
	// wait for their request mutex or require an existing IPC directory.
	proc.requestMu.Lock()
	killed := make(chan error, 1)
	go func() { killed <- proc.Kill() }()
	if err := receiveCancellationTest(t, killed); err != nil {
		t.Fatal(err)
	}
	proc.requestMu.Unlock()
	if helperMayRun(owner, event.handle) {
		t.Fatal("signaled cancellation event still permits startup")
	}
	// Cancellation is sticky and closing/signaling cannot race into a reused
	// kernel handle. In particular Stop may overlap the waiter's close.
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() { _ = proc.Kill(); event.close() })
	}
	workers.Wait()
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill after close: %v", err)
	}
}

func TestElevatedHelperRejectsCanceledOrObsoleteLaunch(t *testing.T) {
	event, eventName := cancellationTestEvent(t)
	if err := event.signal(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	args := []string{elevatedCoreHelperArgument, dir, eventName, cancellationTestOwner(t), "-", filepath.Join(dir, "must-not-start.exe"), dir, "run"}
	handled, code, err := RunElevatedCoreHelper(args)
	if !handled || code == 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled helper: handled=%v code=%d err=%v", handled, code, err)
	}
	event.close()
	if handled, code, err = RunElevatedCoreHelper(args); !handled || code == 0 || err == nil {
		t.Fatalf("closed capability: handled=%v code=%d err=%v", handled, code, err)
	}
	// Legacy invocations have no cancellation capability and must not launch.
	if handled, code, err = RunElevatedCoreHelper([]string{elevatedCoreHelperArgument, dir, "-", args[5], dir}); !handled || code == 0 || err == nil {
		t.Fatalf("legacy protocol: handled=%v code=%d err=%v", handled, code, err)
	}
}

func TestElevatedHelperOwnerCreationTimePreventsPIDReuse(t *testing.T) {
	creation, err := processCreationTime(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	identity := strconv.Itoa(os.Getpid()) + ":" + strconv.FormatUint(creation+1, 10)
	if owner, err := openHelperOwner(identity); owner != 0 || err == nil {
		if owner != 0 {
			windows.CloseHandle(owner)
		}
		t.Fatalf("accepted reused PID with wrong creation time: handle=%v err=%v", owner, err)
	}
}

func TestElevatedStartAlreadyCanceledNeverCallsShell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	proc, err := startElevatedCoreProcess(ctx, "must-not-start.exe", nil, t.TempDir(), nil)
	if proc != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled elevated start: %v %v", proc, err)
	}
}
