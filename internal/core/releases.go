package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const releasesURL = "https://api.github.com/repos/SagerNet/sing-box/releases"

func officialDownload(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Host == "github.com" && strings.HasPrefix(u.EscapedPath(), "/SagerNet/sing-box/releases/download/") && u.RawQuery == "" && u.Fragment == ""
}
func directTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
}
func releaseClient() *http.Client {
	tr := directTransport()
	// 发行下载可遵循用户的 HTTP(S)_PROXY；本机 API 探测仍强制直连。
	// 这里没有 RPC 的 Bearer 凭据，所有目标和重定向仍限定官方 HTTPS 主机。
	tr.Proxy = http.ProxyFromEnvironment
	return &http.Client{Transport: tr, Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		u := req.URL
		if u.Scheme != "https" || u.User != nil {
			return errors.New("insecure download redirect")
		}
		switch u.Host {
		case "github.com", "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		default:
			return errors.New("download redirect outside official distribution hosts")
		}
	}}
}
func assetName(version string) (string, error) {
	osName := runtime.GOOS
	switch osName {
	case "linux", "windows":
	case "darwin":
		osName = "darwin"
	default:
		return "", fmt.Errorf("unsupported OS: %s", osName)
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64", "386":
	case "arm":
		arch = "armv7"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", arch)
	}
	ext := ".tar.gz"
	if osName == "windows" {
		ext = ".zip"
	}
	return "sing-box-" + version + "-" + osName + "-" + arch + ext, nil
}

func Releases(ctx context.Context, includePrerelease bool) ([]Release, error) {
	client := releaseClient()
	defer client.CloseIdleConnections()
	return releases(ctx, includePrerelease, client, releasesURL)
}
func releases(ctx context.Context, includePrerelease bool, client *http.Client, endpoint string) ([]Release, error) {
	type asset struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	}
	type entry struct {
		Tag        string  `json:"tag_name"`
		Prerelease bool    `json:"prerelease"`
		Draft      bool    `json:"draft"`
		URL        string  `json:"html_url"`
		Assets     []asset `json:"assets"`
	}
	var result []Release
	// Use bounded pagination; old, incompatible releases are ignored, never offered.
	for page := 1; page <= 5; page++ {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("per_page", "10")
		q.Set("page", fmt.Sprint(page))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "BoxPilot")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("release metadata HTTP %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, readErr
		}
		if len(b) > 16<<20 {
			return nil, errors.New("release metadata too large")
		}
		var entries []entry
		if err := json.Unmarshal(b, &entries); err != nil {
			return nil, err
		}
		for _, e := range entries {
			version, err := compatibleVersion(e.Tag)
			if err != nil || e.Draft || (!includePrerelease && (e.Prerelease || strings.Contains(version, "-"))) {
				continue
			}
			name, err := assetName(version)
			if err != nil {
				return nil, err
			}
			for _, a := range e.Assets {
				if a.Name != name || !officialDownload(a.URL) {
					continue
				}
				result = append(result, Release{Version: version, Prerelease: e.Prerelease || strings.Contains(version, "-"), URL: e.URL, AssetName: a.Name, DownloadURL: a.URL, Digest: a.Digest, Size: a.Size})
				break
			}
		}
		if len(entries) < 10 || len(result) >= 8 {
			break
		}
	}
	return result, nil
}

func Install(ctx context.Context, root string, r Release, progress func(done, total int64)) (Installed, error) {
	client := releaseClient()
	defer client.CloseIdleConnections()
	return install(ctx, root, r, progress, client)
}
func install(ctx context.Context, root string, r Release, progress func(int64, int64), client *http.Client) (Installed, error) {
	version, err := compatibleVersion(r.Version)
	if err != nil {
		return Installed{}, err
	}
	name, err := assetName(version)
	if err != nil {
		return Installed{}, err
	}
	if r.AssetName != name || !officialDownload(r.DownloadURL) || r.DownloadURL != "https://github.com/SagerNet/sing-box/releases/download/v"+version+"/"+name {
		return Installed{}, errors.New("unexpected release asset or download source")
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(r.Digest, "sha256:"))
	if !strings.HasPrefix(r.Digest, "sha256:") || err != nil || len(expected) != sha256.Size {
		return Installed{}, ErrDigest
	}
	if r.Size <= 0 || r.Size > maxDownloadSize {
		return Installed{}, errors.New("invalid or excessive download size")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Installed{}, err
	}
	if err = privateDir(root); err != nil {
		return Installed{}, err
	}
	versions := filepath.Join(root, "versions")
	if err = privateDir(versions); err != nil {
		return Installed{}, err
	}
	dest := filepath.Join(versions, version)
	if _, err = os.Lstat(dest); err == nil {
		return Installed{}, ErrAlreadyInstalled
	} else if !os.IsNotExist(err) {
		return Installed{}, err
	}
	// A cross-process exclusive lock prevents a concurrent install replacing a live version.
	lock, err := os.OpenFile(filepath.Join(versions, "."+version+".lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Installed{}, fmt.Errorf("install lock: %w", err)
	}
	lock.Close()
	defer os.Remove(lock.Name())
	downloadDir, err := os.MkdirTemp(versions, ".download-*")
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(downloadDir)
	archivePath := filepath.Join(downloadDir, "archive")
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Installed{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", r.DownloadURL, nil)
	if err != nil {
		f.Close()
		return Installed{}, err
	}
	req.Header.Set("User-Agent", "BoxPilot")
	resp, err := client.Do(req)
	if err != nil {
		f.Close()
		return Installed{}, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		f.Close()
		return Installed{}, fmt.Errorf("asset download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxDownloadSize || (resp.ContentLength >= 0 && resp.ContentLength != r.Size) {
		resp.Body.Close()
		f.Close()
		return Installed{}, errors.New("download size differs from release metadata")
	}
	hash := sha256.New()
	pw := &progressWriter{writer: io.MultiWriter(f, hash), total: r.Size, notify: progress, ctx: ctx}
	if progress != nil {
		progress(0, r.Size)
	}
	_, err = io.Copy(pw, io.LimitReader(resp.Body, r.Size+1))
	resp.Body.Close()
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Installed{}, contextError(ctx, err)
	}
	if pw.done != r.Size {
		return Installed{}, errors.New("download size differs from release metadata")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(expected)) {
		return Installed{}, errors.New("SHA-256 digest mismatch")
	}
	// Never extract into the directory that contains the untrusted downloaded archive.
	staging, err := os.MkdirTemp(versions, ".install-*")
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(staging)
	exe := "sing-box"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	executable := filepath.Join(staging, exe)
	if err = extractArchive(ctx, archivePath, name, executable); err != nil {
		return Installed{}, err
	}
	inspected, err := Inspect(ctx, executable)
	if err != nil {
		return Installed{}, err
	}
	if inspected.Version != version {
		return Installed{}, fmt.Errorf("binary version %s differs from release %s", inspected.Version, version)
	}
	if ctx.Err() != nil {
		return Installed{}, ctx.Err()
	}
	if _, err = os.Lstat(dest); err == nil {
		return Installed{}, ErrAlreadyInstalled
	} else if !os.IsNotExist(err) {
		return Installed{}, err
	}
	if err = os.Rename(staging, dest); err != nil {
		return Installed{}, err
	}
	return Installed{Version: version, Path: filepath.Join(dest, exe)}, nil
}

type progressWriter struct {
	writer      io.Writer
	done, total int64
	notify      func(int64, int64)
	ctx         context.Context
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	if p.done+int64(len(b)) > p.total {
		return 0, errors.New("download exceeds declared size")
	}
	n, err := p.writer.Write(b)
	p.done += int64(n)
	if p.notify != nil {
		p.notify(p.done, p.total)
	}
	return n, err
}
func InstalledVersions(root string) ([]Installed, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	versions := filepath.Join(root, "versions")
	for _, path := range []string{root, versions} {
		info, statErr := os.Lstat(path)
		if os.IsNotExist(statErr) {
			return []Installed{}, nil
		}
		if statErr != nil {
			return nil, statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe versions directory")
		}
	}
	entries, err := os.ReadDir(versions)
	if os.IsNotExist(err) {
		return []Installed{}, nil
	}
	if err != nil {
		return nil, err
	}
	var result []Installed
	exe := "sing-box"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		v, err := compatibleVersion(e.Name())
		if err != nil || v != e.Name() {
			continue
		}
		path := filepath.Join(root, "versions", v, exe)
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() {
			result = append(result, Installed{Version: v, Path: path})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	return result, nil
}
