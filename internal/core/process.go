package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type managedProcess interface {
	PID() int
	Wait() error
	GracefulStop() error
	Kill() error
}

// 同时返回进程和错误表示仍有后台回收任务；即使上下文已过期，调用方也必须
// 等待 Wait 完成后再删除运行目录。此类进程不会转为正常运行状态。
type processStarter func(context.Context, string, []string, string, io.Writer, bool, []uint32) (managedProcess, error)

type processStartResult struct {
	proc managedProcess
	err  error
}

// startProcessContext 接管无法中断的系统启动调用（例如 UAC）。取消后将结果
// 移交回收器，返回仅用于等待回收的进程，让 Manager 保留运行目录。
// revoke 必须永久撤销本次启动核心的权限，不能放弃迟到的启动结果。
func startProcessContext(ctx context.Context, launch func() (managedProcess, error), revoke func()) (managedProcess, error) {
	if err := ctx.Err(); err != nil {
		revoke()
		return nil, err
	}
	result := make(chan processStartResult, 1)
	go func() {
		proc, err := launch()
		result <- processStartResult{proc, err}
	}()
	select {
	case started := <-result:
		err := contextError(ctx, started.err)
		if err == nil {
			return started.proc, nil
		}
		revoke()
		if started.proc == nil {
			return nil, err
		}
		result <- started
		return reapProcessStart(result), err
	case <-ctx.Done():
		revoke()
		return reapProcessStart(result), ctx.Err()
	}
}

type reapingProcess struct {
	done chan struct{}
	err  error
}

func reapProcessStart(result <-chan processStartResult) managedProcess {
	p := &reapingProcess{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		started := <-result
		if started.proc != nil {
			// 仅使用已持有的进程或句柄，绝不按 PID 重新查找进程。
			_ = started.proc.Kill()
			p.err = started.proc.Wait()
		}
	}()
	return p
}

func (*reapingProcess) PID() int            { return 0 }
func (*reapingProcess) GracefulStop() error { return nil }
func (*reapingProcess) Kill() error         { return nil } // 回收已经不可撤销。
func (p *reapingProcess) Wait() error       { <-p.done; return p.err }

type boundedOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) >= maxOutputSize {
		b.data = append(b.data[:0], p[len(p)-maxOutputSize:]...)
	} else {
		b.data = append(b.data, p...)
		if len(b.data) > maxOutputSize {
			b.data = append(b.data[:0], b.data[len(b.data)-maxOutputSize:]...)
		}
	}
	return n, nil
}
func (b *boundedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }
func binaryPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("binary path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("binary must be a regular file")
	}
	return resolved, nil
}
func commandOutput(ctx context.Context, binary string, args ...string) (string, error) {
	path, err := binaryPath(binary)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	return output.String(), contextError(ctx, err)
}

var reportedVersion = regexp.MustCompile(`(?m)^sing-box version ([^\s]+)\s*$`)

func Inspect(ctx context.Context, path string) (Installed, error) {
	absolute, err := binaryPath(path)
	if err != nil {
		return Installed{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := commandOutput(ctx, absolute, "version")
	if err != nil {
		return Installed{}, fmt.Errorf("sing-box version: %w: %s", err, output)
	}
	match := reportedVersion.FindStringSubmatch(output)
	if match == nil {
		return Installed{}, errors.New("not a recognized sing-box binary")
	}
	version, err := compatibleVersion(match[1])
	if err != nil {
		return Installed{}, err
	}
	return Installed{Version: version, Path: absolute}, nil
}
func Check(ctx context.Context, binary, configPath string) (string, error) {
	if !filepath.IsAbs(configPath) {
		var err error
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return "", err
		}
	}
	if _, err := readLimited(configPath, maxConfigSize); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Match run's private working directory for relative configuration resources.
	path, err := binaryPath(binary)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, "check", "-c", configPath)
	cmd.Dir = filepath.Dir(configPath)
	cmd.WaitDelay = time.Second
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	if err != nil {
		return output.String(), fmt.Errorf("sing-box check: %w", contextError(ctx, err))
	}
	return output.String(), nil
}

var bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s"'\\]+`)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func redact(text, secret string) string {
	text = ansiPattern.ReplaceAllString(text, "")
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
		// A bounded buffer or a concurrent write may split a token at either edge.
		for n := len(secret) - 1; n >= 4; n-- {
			if strings.HasSuffix(text, secret[:n]) {
				text = strings.TrimSuffix(text, secret[:n]) + "[REDACTED]"
			}
			if strings.HasPrefix(text, secret[len(secret)-n:]) {
				text = "[REDACTED]" + strings.TrimPrefix(text, secret[len(secret)-n:])
			}
		}
	}
	return bearerPattern.ReplaceAllString(text, "${1}[REDACTED]")
}
