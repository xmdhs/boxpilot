//go:build windows

package core

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type windowsCommandProcess struct {
	cmd *exec.Cmd
}

func startCoreProcess(ctx context.Context, path string, args []string, dir string, output io.Writer, elevated bool, cpuSetIDs []uint32) (managedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if elevated {
		return startElevatedCoreProcess(ctx, path, args, dir, cpuSetIDs)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	return startProcessContext(ctx, func() (managedProcess, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &windowsCommandProcess{cmd: cmd}, nil
	}, func() {})
}

func (p *windowsCommandProcess) PID() int            { return p.cmd.Process.Pid }
func (p *windowsCommandProcess) Wait() error         { return p.cmd.Wait() }
func (p *windowsCommandProcess) GracefulStop() error { return gracefulTerminate(p.cmd.Process) }
func (p *windowsCommandProcess) Kill() error         { return p.cmd.Process.Kill() }

func gracefulTerminate(p *os.Process) error {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	result, _, err := proc.Call(1, uintptr(p.Pid))
	if result == 0 {
		return err
	}
	return nil
}

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     windows.Handle
}

const (
	seeMaskNoAsync        = 0x00000100
	seeMaskNoCloseProcess = 0x00000040
	errorCancelled        = 1223
	swHide                = 0
)
