//go:build windows

package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"
)

// These tests never invoke ShellExecuteExW, UAC, a GUI, or a real core.
func TestElevatedActionValidatesProcessCreationIdentity(t *testing.T) {
	creation, err := processCreationTime(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	args := []string{elevatedActionArgument, "terminate", strconv.Itoa(os.Getpid()), strconv.FormatUint(creation+1, 10), "-"}
	handled, code, err := RunElevatedCoreAction(args)
	if !handled || code == 0 || err == nil {
		t.Fatalf("wrong creation time accepted: handled=%v code=%d err=%v", handled, code, err)
	}
}

func TestElevatedActionRejectsMalformedArguments(t *testing.T) {
	for _, args := range [][]string{{elevatedActionArgument}, {elevatedActionArgument, "terminate", "0", "1", "-"}, {elevatedActionArgument, "processor-affinity", "1", "1", "x"}, {elevatedActionArgument, "start-core", "not-base64"}} {
		handled, code, err := RunElevatedCoreAction(args)
		if !handled || code == 0 || err == nil {
			t.Errorf("accepted malformed elevated action %v: handled=%v code=%d err=%v", args, handled, code, err)
		}
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

func TestPublishElevatedCoreStartResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.json")
	want := elevatedCoreStartResult{PID: 1234, Creation: 5678}
	if err := publishElevatedCoreStartResult(path, want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got elevatedCoreStartResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("published result = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary result still exists: %v", err)
	}
}
