//go:build windows

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const elevatedActionArgument = "--boxpilot-elevated-action"

type elevatedWindowsProcess struct {
	handle   windows.Handle
	pid      int
	creation uint64
}

func (p *elevatedWindowsProcess) PID() int { return p.pid }
func (p *elevatedWindowsProcess) Wait() error {
	defer windows.CloseHandle(p.handle)
	_, err := windows.WaitForSingleObject(p.handle, windows.INFINITE)
	if err != nil {
		return err
	}
	var code uint32
	if err = windows.GetExitCodeProcess(p.handle, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("elevated sing-box exited with code %d", code)
	}
	return nil
}
func (p *elevatedWindowsProcess) GracefulStop() error {
	return errors.New("graceful stop is unavailable for an elevated process")
}
func (p *elevatedWindowsProcess) Kill() error {
	return runElevatedAction("terminate", p.pid, p.creation, nil)
}
func (p *elevatedWindowsProcess) SetCPUAffinity(ids []uint32) error {
	return runElevatedAction("processor-affinity", p.pid, p.creation, ids)
}

func processCreationTime(handle windows.Handle) (uint64, error) {
	var creation, exit, kernel, user windows.Filetime
	err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), err
}

// RunElevatedCoreAction handles one-shot, UAC-elevated process operations.
// It reopens by PID only to verify identity, then operates on that held handle.
func RunElevatedCoreAction(args []string) (bool, int, error) {
	if len(args) == 0 || args[0] != elevatedActionArgument {
		return false, 0, nil
	}
	if len(args) != 5 {
		return true, 1, errors.New("invalid elevated process action arguments")
	}
	pid, err := strconv.ParseUint(args[2], 10, 32)
	if err != nil || pid == 0 {
		return true, 1, errors.New("invalid sing-box process ID")
	}
	creation, err := strconv.ParseUint(args[3], 10, 64)
	if err != nil || creation == 0 {
		return true, 1, errors.New("invalid sing-box process identity")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|0x0200, false, uint32(pid))
	if err != nil {
		return true, 1, fmt.Errorf("open managed sing-box process: %w", err)
	}
	defer windows.CloseHandle(handle)
	actual, err := processCreationTime(handle)
	if err != nil || actual != creation {
		return true, 1, errors.New("managed sing-box process identity changed")
	}
	switch args[1] {
	case "terminate":
		if err = windows.TerminateProcess(handle, 1); err != nil {
			return true, 1, fmt.Errorf("terminate managed sing-box: %w", err)
		}
	case "processor-affinity":
		ids, parseErr := parseCPUSetIDs(args[4])
		if parseErr != nil {
			return true, 1, parseErr
		}
		if err = setProcessAffinityHandle(handle, ids); err != nil {
			return true, 1, err
		}
	default:
		return true, 1, errors.New("unsupported elevated process action")
	}
	return true, 0, nil
}

// Launch sing-box itself through UAC; Manager.Start applies configured affinity
// afterward through a separate elevated action, as in the v0.0.3 flow.
func startElevatedCoreProcess(ctx context.Context, path string, args []string, dir string, _ []uint32) (managedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := binaryPath(path)
	if err != nil {
		return nil, err
	}
	return launchRunAs(ctx, binary, args, dir)
}

func runElevatedAction(action string, pid int, creation uint64, ids []uint32) error {
	args := []string{elevatedActionArgument, action, strconv.Itoa(pid), strconv.FormatUint(creation, 10), encodeCPUSetIDs(ids)}
	proc, err := launchRunAs(context.Background(), "", args, "")
	if err != nil {
		return err
	}
	return proc.Wait()
}

func encodeCPUSetIDs(ids []uint32) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return strings.Join(parts, ",")
}

func launchRunAs(ctx context.Context, target string, args []string, dir string) (managedProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate BoxPilot executable for elevated operation: %w", err)
	}
	fileName := target
	parameters := args
	if target == "" {
		fileName = executable
		parameters = append([]string{elevatedActionArgument}, args[1:]...)
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, err := syscall.UTF16PtrFromString(fileName)
	if err != nil {
		return nil, err
	}
	var command strings.Builder
	for i, arg := range parameters {
		if i > 0 {
			command.WriteByte(' ')
		}
		command.WriteString(syscall.EscapeArg(arg))
	}
	paramPtr, err := syscall.UTF16PtrFromString(command.String())
	if err != nil {
		return nil, err
	}
	var dirPtr *uint16
	if dir != "" {
		dirPtr, err = syscall.UTF16PtrFromString(dir)
		if err != nil {
			return nil, err
		}
	}
	var info shellExecuteInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	info.fMask = seeMaskNoCloseProcess | seeMaskNoAsync
	info.lpVerb = verb
	info.lpFile = file
	info.lpParameters = paramPtr
	info.lpDirectory = dirPtr
	info.nShow = swHide
	return startProcessContext(ctx, func() (managedProcess, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		proc := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW")
		ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(&info)))
		if ok == 0 {
			if errno, ok := callErr.(syscall.Errno); ok && errno == errorCancelled {
				return nil, errors.New("administrator approval was canceled")
			}
			return nil, fmt.Errorf("start elevated process: %w", callErr)
		}
		if info.hProcess == 0 {
			return nil, errors.New("Windows did not return elevated process handle")
		}
		pid, err := windows.GetProcessId(info.hProcess)
		if err != nil {
			windows.CloseHandle(info.hProcess)
			return nil, err
		}
		creation, err := processCreationTime(info.hProcess)
		if err != nil {
			windows.CloseHandle(info.hProcess)
			return nil, err
		}
		return &elevatedWindowsProcess{handle: info.hProcess, pid: int(pid), creation: creation}, nil
	}, func() {})
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
