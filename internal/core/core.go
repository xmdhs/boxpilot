// Package core manages only binaries, configuration, and processes owned by BoxPilot.
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Release struct {
	Version     string
	Prerelease  bool
	URL         string
	AssetName   string
	DownloadURL string
	Digest      string
	Size        int64
}
type Installed struct{ Version, Path string }
type Profile struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Path               string    `json:"path,omitempty"`
	Type               string    `json:"type"`
	RemoteURL          string    `json:"remoteURL,omitempty"`
	AutoUpdate         bool      `json:"autoUpdate,omitempty"`
	AutoUpdateInterval int       `json:"autoUpdateInterval,omitempty"`
	LastUpdated        time.Time `json:"lastUpdated,omitempty"`
}
type State struct {
	Phase       string
	Error       string
	Version     string
	PID         int
	URL, Secret string
	Output      string
}

type Controller interface {
	Start(context.Context, string, Profile, bool) error
	Stop(context.Context) error
	State() State
}

var (
	ErrAPIConflict          = errors.New("configuration already contains an API service")
	ErrBusy                 = errors.New("core is already running or changing state")
	ErrIncompatible         = errors.New("sing-box version must support API service (1.14 or newer in major version 1)")
	ErrDigest               = errors.New("missing or invalid SHA-256 digest")
	ErrAlreadyInstalled     = errors.New("version is already installed; it will not be overwritten")
	ErrElevationUnsupported = errors.New("elevated core execution is only supported on Windows")
)

const (
	maxDownloadSize int64 = 256 << 20
	maxExtractSize  int64 = 512 << 20
	maxConfigSize   int64 = 8 << 20
	maxOutputSize         = 64 << 10
)

var versionPattern = regexp.MustCompile(`^v?(1)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

func compatibleVersion(v string) (string, error) {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return "", ErrIncompatible
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil || minor < 14 {
		return "", ErrIncompatible
	}
	return strings.TrimPrefix(v, "v"), nil
}
func randomID() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a private directory: %s", path)
	}
	return os.Chmod(path, 0700)
}
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func readLimited(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("file size limit exceeded")
	}
	return b, err
}
func contextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
