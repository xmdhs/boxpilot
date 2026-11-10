//go:build !windows

package core

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd)        {}
func gracefulTerminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
