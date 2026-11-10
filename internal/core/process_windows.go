package core

import (
	"os"
	"os/exec"
	"syscall"
)

// A separate process group lets us address only our child, never other consoles.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
func gracefulTerminate(p *os.Process) error {
	// Windows has no SIGTERM. Best effort CTRL_BREAK requires an attached console;
	// Manager falls back to terminating this exact process handle after its grace period.
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	result, _, err := proc.Call(1, uintptr(p.Pid))
	if result == 0 {
		return err
	}
	return nil
}
