//go:build windows

package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	elevatedCoreHelperArgument = "--boxpilot-elevated-core-helper"
	elevatedCorePIDFile        = "helper.pid"
	elevatedCoreCommandFile    = "helper.command"
	elevatedCoreResultFile     = "helper.result"
)

type elevatedCoreCommand struct {
	Request   string   `json:"request"`
	Action    string   `json:"action"`
	CPUSetIDs []uint32 `json:"cpuSetIDs,omitempty"`
}

type elevatedCoreResult struct {
	Request string `json:"request"`
	Error   string `json:"error,omitempty"`
}

type elevatedWindowsProcess struct {
	handle    windows.Handle
	pid       int
	ipcDir    string
	once      sync.Once
	waitErr   error
	done      chan struct{}
	requestMu sync.Mutex
	sequence  uint64
}

func (p *elevatedWindowsProcess) PID() int { return p.pid }

func (p *elevatedWindowsProcess) Wait() error {
	p.once.Do(func() {
		defer windows.CloseHandle(p.handle)
		defer close(p.done)
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
	return errors.New("graceful stop is unavailable for an elevated process")
}

func (p *elevatedWindowsProcess) Kill() error {
	return p.request(elevatedCoreCommand{Action: "kill"}, false)
}

func (p *elevatedWindowsProcess) SetCPUSetIDs(ids []uint32) error {
	return p.request(elevatedCoreCommand{Action: "set-cpu-sets", CPUSetIDs: ids}, true)
}

func (p *elevatedWindowsProcess) request(command elevatedCoreCommand, waitResult bool) error {
	p.requestMu.Lock()
	defer p.requestMu.Unlock()
	select {
	case <-p.done:
		return ErrNotRunning
	default:
	}
	p.sequence++
	command.Request = strconv.FormatUint(p.sequence, 10)
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	commandPath := filepath.Join(p.ipcDir, elevatedCoreCommandFile)
	resultPath := filepath.Join(p.ipcDir, elevatedCoreResultFile)
	_ = os.Remove(resultPath)
	if err = atomicWrite(commandPath, data); err != nil {
		return fmt.Errorf("send elevated sing-box command: %w", err)
	}
	if !waitResult {
		return nil
	}

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-p.done:
			return ErrNotRunning
		case <-timer.C:
			return errors.New("timed out waiting for elevated CPU scheduling result")
		case <-ticker.C:
			resultData, readErr := os.ReadFile(resultPath)
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return fmt.Errorf("read elevated CPU scheduling result: %w", readErr)
			}
			var result elevatedCoreResult
			if err = json.Unmarshal(resultData, &result); err != nil {
				return fmt.Errorf("decode elevated CPU scheduling result: %w", err)
			}
			if result.Request != command.Request {
				continue
			}
			_ = os.Remove(resultPath)
			if result.Error != "" {
				return errors.New(result.Error)
			}
			return nil
		}
	}
}

func RunElevatedCoreHelper(args []string) (bool, int, error) {
	if len(args) == 0 || args[0] != elevatedCoreHelperArgument {
		return false, 0, nil
	}
	if len(args) < 5 {
		return true, 1, errors.New("invalid elevated core helper arguments")
	}
	ipcDir, binary, workingDir := args[1], args[3], args[4]
	if !filepath.IsAbs(ipcDir) || !filepath.IsAbs(binary) || !filepath.IsAbs(workingDir) {
		return true, 1, errors.New("elevated core helper paths must be absolute")
	}
	info, err := os.Lstat(ipcDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return true, 1, errors.New("invalid elevated core helper directory")
	}
	binary, err = binaryPath(binary)
	if err != nil {
		return true, 1, err
	}
	cpuSetIDs, err := parseCPUSetIDs(args[2])
	if err != nil {
		return true, 1, err
	}
	command := exec.Command(binary, args[5:]...)
	command.Dir = workingDir
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err = command.Start(); err != nil {
		return true, 1, fmt.Errorf("start elevated sing-box: %w", err)
	}
	pid := command.Process.Pid
	if len(cpuSetIDs) > 0 {
		if err = setProcessCPUSetIDs(pid, cpuSetIDs); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return true, 1, fmt.Errorf("apply saved CPU scheduling to elevated sing-box: %w", err)
		}
	}
	pidPath := filepath.Join(ipcDir, elevatedCorePIDFile)
	if err = atomicWrite(pidPath, []byte(strconv.Itoa(pid))); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return true, 1, fmt.Errorf("publish elevated sing-box process ID: %w", err)
	}

	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	commandPath := filepath.Join(ipcDir, elevatedCoreCommandFile)
	resultPath := filepath.Join(ipcDir, elevatedCoreResultFile)
	for {
		select {
		case waitErr := <-wait:
			_ = os.Remove(pidPath)
			_ = os.Remove(commandPath)
			_ = os.Remove(resultPath)
			return true, processExitCode(waitErr), nil
		case <-ticker.C:
			commandData, readErr := os.ReadFile(commandPath)
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			if readErr != nil {
				continue
			}
			_ = os.Remove(commandPath)
			var request elevatedCoreCommand
			if err = json.Unmarshal(commandData, &request); err != nil || request.Request == "" {
				continue
			}
			switch request.Action {
			case "set-cpu-sets":
				result := elevatedCoreResult{Request: request.Request}
				if setErr := setProcessCPUSetIDs(pid, request.CPUSetIDs); setErr != nil {
					result.Error = setErr.Error()
				}
				resultData, marshalErr := json.Marshal(result)
				if marshalErr == nil {
					_ = atomicWrite(resultPath, resultData)
				}
			case "kill":
				_ = command.Process.Kill()
				waitErr := <-wait
				_ = os.Remove(pidPath)
				_ = os.Remove(resultPath)
				return true, processExitCode(waitErr), nil
			}
		}
	}
}

func parseCPUSetIDs(value string) ([]uint32, error) {
	if value == "" || value == "-" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	ids := make([]uint32, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid CPU Set ID: %w", err)
		}
		ids = append(ids, uint32(id))
	}
	return ids, nil
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func startElevatedCoreProcess(path string, args []string, dir string, cpuSetIDs []uint32) (managedProcess, error) {
	if _, err := binaryPath(path); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate BoxPilot executable for elevated core launch: %w", err)
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(cpuSetIDs))
	for i, id := range cpuSetIDs {
		ids[i] = strconv.FormatUint(uint64(id), 10)
	}
	idsArg := strings.Join(ids, ",")
	if idsArg == "" {
		idsArg = "-"
	}
	helperArgs := []string{elevatedCoreHelperArgument, dir, idsArg, path, dir}
	helperArgs = append(helperArgs, args...)
	var command strings.Builder
	for i, arg := range helperArgs {
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
	pidPath := filepath.Join(dir, elevatedCorePIDFile)
	_ = os.Remove(pidPath)
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
		return nil, fmt.Errorf("start elevated sing-box helper: %w", callErr)
	}
	if info.hProcess == 0 {
		return nil, errors.New("Windows did not return the elevated helper process handle")
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, readErr := os.ReadFile(pidPath); readErr == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return &elevatedWindowsProcess{handle: info.hProcess, pid: pid, ipcDir: dir, done: make(chan struct{})}, nil
			}
		}
		wait, waitErr := windows.WaitForSingleObject(info.hProcess, 0)
		if waitErr != nil {
			windows.CloseHandle(info.hProcess)
			return nil, fmt.Errorf("check elevated sing-box helper: %w", waitErr)
		}
		if wait == windows.WAIT_OBJECT_0 {
			var code uint32
			_ = windows.GetExitCodeProcess(info.hProcess, &code)
			windows.CloseHandle(info.hProcess)
			return nil, fmt.Errorf("elevated sing-box helper exited during startup (code %d)", code)
		}
		select {
		case <-deadline.C:
			_ = windows.TerminateProcess(info.hProcess, 1)
			windows.CloseHandle(info.hProcess)
			return nil, errors.New("timed out waiting for elevated sing-box startup")
		case <-ticker.C:
		}
	}
}
