//go:build windows

package core

import (
	"context"
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

// 每次启动独立的手动复位事件既是取消信号，也是启动凭证。迟到的 helper
// 必须先打开此事件；所有者关闭事件后，启动将被拒绝。取消和停止均不依赖
// 文件写入或 helper 命令锁。
type elevatedCancelEvent struct {
	mu     sync.Mutex
	handle windows.Handle
}

func (e *elevatedCancelEvent) signal() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handle == 0 {
		return nil
	}
	return windows.SetEvent(e.handle)
}

func (e *elevatedCancelEvent) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handle != 0 {
		_ = windows.CloseHandle(e.handle)
		e.handle = 0
	}
}

func processCreationTime(handle windows.Handle) (uint64, error) {
	var creation, exit, kernel, user windows.Filetime
	err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), err
}

func openHelperOwner(identity string) (windows.Handle, error) {
	pidText, creationText, ok := strings.Cut(identity, ":")
	pid, pidErr := strconv.ParseUint(pidText, 10, 32)
	creation, creationErr := strconv.ParseUint(creationText, 10, 64)
	if !ok || pidErr != nil || creationErr != nil || pid == 0 {
		return 0, errors.New("invalid elevated helper owner identity")
	}
	owner, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, fmt.Errorf("open elevated helper owner: %w", err)
	}
	actual, err := processCreationTime(owner)
	if err != nil || actual != creation {
		_ = windows.CloseHandle(owner)
		return 0, errors.New("elevated helper owner no longer exists")
	}
	return owner, nil
}

func helperMayRun(owner, cancelEvent windows.Handle) bool {
	// 校验创建时间后持有真实进程句柄，避免 PID 复用改变旧 helper 的所有者。
	ownerState, ownerErr := windows.WaitForSingleObject(owner, 0)
	cancelState, cancelErr := windows.WaitForSingleObject(cancelEvent, 0)
	return ownerErr == nil && cancelErr == nil && ownerState == uint32(windows.WAIT_TIMEOUT) && cancelState == uint32(windows.WAIT_TIMEOUT)
}

type elevatedWindowsProcess struct {
	handle      windows.Handle
	cancelEvent *elevatedCancelEvent
	pid         int
	ipcDir      string
	once        sync.Once
	waitErr     error
	done        chan struct{}
	requestMu   sync.Mutex
	sequence    uint64
}

func (p *elevatedWindowsProcess) PID() int { return p.pid }

func (p *elevatedWindowsProcess) Wait() error {
	p.once.Do(func() {
		defer p.cancelEvent.close()
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
	return p.cancelEvent.signal()
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
	if len(args) < 7 {
		return true, 1, errors.New("invalid elevated core helper arguments")
	}
	// 协议依次为目录、取消凭证、所有者 PID:创建时间、CPU Sets、
	// 核心路径、工作目录及核心参数。
	ipcDir, binary, workingDir := args[1], args[5], args[6]
	eventName, err := windows.UTF16PtrFromString(args[2])
	if err != nil {
		return true, 1, err
	}
	cancelEvent, err := windows.OpenEvent(windows.SYNCHRONIZE, false, eventName)
	if err != nil {
		return true, 1, fmt.Errorf("open elevated launch cancellation event: %w", err)
	}
	defer windows.CloseHandle(cancelEvent)
	owner, err := openHelperOwner(args[3])
	if err != nil {
		return true, 1, err
	}
	defer windows.CloseHandle(owner)
	if !helperMayRun(owner, cancelEvent) {
		return true, 1, context.Canceled
	}
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
	cpuSetIDs, err := parseCPUSetIDs(args[4])
	if err != nil {
		return true, 1, err
	}
	command := exec.Command(binary, args[7:]...)
	command.Dir = workingDir
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if !helperMayRun(owner, cancelEvent) {
		return true, 1, context.Canceled
	}
	if err = command.Start(); err != nil {
		return true, 1, fmt.Errorf("start elevated sing-box: %w", err)
	}
	// 取消可能与 CreateProcess 竞争；即使尚未公布 PID，也只终止并回收
	// 此次启动实际持有句柄的子进程。
	if !helperMayRun(owner, cancelEvent) {
		_ = command.Process.Kill()
		_ = command.Wait()
		return true, 1, context.Canceled
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
			if !helperMayRun(owner, cancelEvent) {
				_ = command.Process.Kill()
				waitErr := <-wait
				_ = os.Remove(pidPath)
				_ = os.Remove(commandPath)
				_ = os.Remove(resultPath)
				return true, processExitCode(waitErr), nil
			}
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

func startElevatedCoreProcess(ctx context.Context, path string, args []string, dir string, cpuSetIDs []uint32) (managedProcess, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := binaryPath(path); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate BoxPilot executable for elevated core launch: %w", err)
	}
	token, err := randomID()
	if err != nil {
		return nil, err
	}
	eventName := `Local\BoxPilot-core-` + token
	eventNamePtr, _ := windows.UTF16PtrFromString(eventName)
	eventHandle, err := windows.CreateEvent(nil, 1, 0, eventNamePtr)
	if err != nil {
		return nil, fmt.Errorf("create elevated launch cancellation event: %w", err)
	}
	cancelEvent := &elevatedCancelEvent{handle: eventHandle}
	transferred := false
	defer func() {
		if !transferred {
			cancelEvent.close()
		}
	}()
	creation, err := processCreationTime(windows.CurrentProcess())
	if err != nil {
		return nil, fmt.Errorf("identify elevated helper owner: %w", err)
	}
	ownerIdentity := strconv.Itoa(os.Getpid()) + ":" + strconv.FormatUint(creation, 10)
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
	helperArgs := []string{elevatedCoreHelperArgument, dir, eventName, ownerIdentity, idsArg, path, dir}
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
	started, err := startProcessContext(ctx, func() (managedProcess, error) {
		owned := false
		defer func() {
			if !owned {
				_ = cancelEvent.signal()
				cancelEvent.close()
			}
		}()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Go context 无法中断 ShellExecuteExW/UAC；相关内存与迟到的
		// 进程句柄始终由此 goroutine 持有。
		proc := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW")
		ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(&info)))
		var p *elevatedWindowsProcess
		if info.hProcess != 0 {
			p = &elevatedWindowsProcess{handle: info.hProcess, cancelEvent: cancelEvent, ipcDir: dir, done: make(chan struct{})}
			owned = true
		}
		if ok == 0 {
			var launchErr error
			if errno, isErrno := callErr.(syscall.Errno); isErrno && errno == errorCancelled {
				launchErr = errors.New("administrator approval was canceled")
			} else {
				launchErr = fmt.Errorf("start elevated sing-box helper: %w", callErr)
			}
			if p != nil {
				return p, launchErr
			}
			return nil, launchErr
		}
		if p == nil {
			return nil, errors.New("Windows did not return the elevated helper process handle")
		}
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if err := ctx.Err(); err != nil {
				return p, err
			}
			if data, readErr := os.ReadFile(pidPath); readErr == nil {
				pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
				if parseErr == nil && pid > 0 {
					p.pid = pid
					return p, nil
				}
			}
			wait, waitErr := windows.WaitForSingleObject(p.handle, 0)
			if waitErr != nil {
				return p, fmt.Errorf("check elevated sing-box helper: %w", waitErr)
			}
			if wait == windows.WAIT_OBJECT_0 {
				var code uint32
				_ = windows.GetExitCodeProcess(p.handle, &code)
				return p, fmt.Errorf("elevated sing-box helper exited during startup (code %d)", code)
			}
			select {
			case <-ctx.Done():
				return p, ctx.Err()
			case <-ticker.C:
			}
		}
	}, func() { _ = cancelEvent.signal() })
	transferred = started != nil
	return started, err
}
