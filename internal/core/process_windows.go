//go:build windows

package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsCommandProcess struct {
	cmd *exec.Cmd
}

type elevatedWindowsProcess struct {
	handle  windows.Handle
	pid     int
	once    sync.Once
	waitErr error
}

func startCoreProcess(path string, args []string, dir string, output io.Writer, elevated bool) (managedProcess, error) {
	if elevated {
		return startElevatedCoreProcess(path, args, dir)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &windowsCommandProcess{cmd: cmd}, nil
}

func (p *windowsCommandProcess) PID() int            { return p.cmd.Process.Pid }
func (p *windowsCommandProcess) Wait() error         { return p.cmd.Wait() }
func (p *windowsCommandProcess) GracefulStop() error { return gracefulTerminate(p.cmd.Process) }
func (p *windowsCommandProcess) Kill() error         { return p.cmd.Process.Kill() }

func (p *elevatedWindowsProcess) PID() int { return p.pid }
func (p *elevatedWindowsProcess) Wait() error {
	p.once.Do(func() {
		defer windows.CloseHandle(p.handle)
		_, p.waitErr = windows.WaitForSingleObject(p.handle, windows.INFINITE)
		if p.waitErr != nil {
			return
		}
		var code uint32
		p.waitErr = windows.GetExitCodeProcess(p.handle, &code)
		if p.waitErr == nil && code != 0 {
			p.waitErr = fmt.Errorf("elevated sing-box exited with code %d", code)
		}
	})
	return p.waitErr
}
func (p *elevatedWindowsProcess) GracefulStop() error {
	// ShellExecuteEx starts the elevated process outside our console group.
	// There is no reliable graceful control channel, so Manager uses its bounded
	// stop timeout and then terminates this exact process handle.
	return errors.New("graceful stop is unavailable for an elevated process")
}
func (p *elevatedWindowsProcess) Kill() error {
	return windows.TerminateProcess(p.handle, 1)
}

func startElevatedCoreProcess(path string, args []string, dir string) (managedProcess, error) {
	if _, err := binaryPath(path); err != nil {
		return nil, err
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var command strings.Builder
	for i, arg := range args {
		if i > 0 {
			command.WriteByte(' ')
		}
		command.WriteString(syscall.EscapeArg(arg))
	}
	parameters, err := syscall.UTF16PtrFromString(command.String())
	if err != nil {
		return nil, err
	}
	workingDirectory, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	var info shellExecuteInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	info.fMask = seeMaskNoCloseProcess | seeMaskNoAsync
	info.lpVerb = verb
	info.lpFile = file
	info.lpParameters = parameters
	info.lpDirectory = workingDirectory
	info.nShow = swHide
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW")
	ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errno, isErrno := callErr.(syscall.Errno); isErrno && errno == errorCancelled {
			return nil, errors.New("administrator approval was canceled")
		}
		return nil, fmt.Errorf("start sing-box with administrator approval: %w", callErr)
	}
	if info.hProcess == 0 {
		return nil, errors.New("Windows did not return the elevated sing-box process handle")
	}
	pid, err := windows.GetProcessId(info.hProcess)
	if err != nil {
		windows.CloseHandle(info.hProcess)
		return nil, err
	}
	return &elevatedWindowsProcess{handle: info.hProcess, pid: int(pid)}, nil
}

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
