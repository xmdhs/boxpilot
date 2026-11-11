package core

import (
	"context"
	"errors"
	"fmt"
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
