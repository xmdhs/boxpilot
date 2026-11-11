//go:build !windows

package core

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type commandProcess struct{ cmd *exec.Cmd }

func startCoreProcess(ctx context.Context, path string, args []string, dir string, output io.Writer, elevated bool, _ []uint32) (managedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if elevated {
		return nil, ErrElevationUnsupported
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = time.Second
	return startProcessContext(ctx, func() (managedProcess, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &commandProcess{cmd: cmd}, nil
	}, func() {})
}

func gracefulTerminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }

func (p *commandProcess) PID() int            { return p.cmd.Process.Pid }
func (p *commandProcess) Wait() error         { return p.cmd.Wait() }
func (p *commandProcess) GracefulStop() error { return gracefulTerminate(p.cmd.Process) }
func (p *commandProcess) Kill() error         { return p.cmd.Process.Kill() }
