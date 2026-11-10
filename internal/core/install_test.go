package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type archiveEntry struct {
	name string
	data []byte
	kind byte
	mode os.FileMode
	size int64
}

func makeArchive(t *testing.T, asset string, entries []archiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	if strings.HasSuffix(asset, ".zip") {
		w := zip.NewWriter(&b)
		for _, e := range entries {
			h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
			mode := e.mode
			if mode == 0 {
				mode = 0700
			}
			h.SetMode(mode)
			f, err := w.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&b)
		w := tar.NewWriter(gz)
		for _, e := range entries {
			kind := e.kind
			if kind == 0 {
				kind = tar.TypeReg
			}
			size := int64(len(e.data))
			if e.size != 0 {
				size = e.size
			}
			h := &tar.Header{Name: e.name, Mode: 0700, Size: size, Typeflag: kind, Linkname: "target"}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
		// Deliberately allow a truncated, oversized entry fixture.
		_ = w.Close()
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}
func fixtureRelease(t *testing.T, data []byte) Release {
	t.Helper()
	name, err := assetName("1.14.4")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return Release{Version: "1.14.4", AssetName: name, DownloadURL: "https://github.com/SagerNet/sing-box/releases/download/v1.14.4/" + name, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(data))}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func localAssetClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !officialDownload(r.URL.String()) {
			return nil, fmt.Errorf("unexpected test download %s", r.URL)
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		copy.URL = &u
		copy.Host = target.Host
		return transport.RoundTrip(copy)
	})}
}
func TestReleasesFiltersPlatformAndCompatibility(t *testing.T) {
	name, _ := assetName("1.14.4")
	preName, _ := assetName("1.15.0-beta.1")
	asset := func(name, host string) map[string]any {
		return map[string]any{"name": name, "browser_download_url": host + "/SagerNet/sing-box/releases/download/v1.14.4/" + name, "digest": "sha256:" + strings.Repeat("a", 64), "size": 123}
	}
	entries := []map[string]any{
		{"tag_name": "v1.14.4", "html_url": "https://github.com/SagerNet/sing-box/releases/tag/v1.14.4", "assets": []any{asset("wrong-platform", "https://github.com"), asset(name, "https://github.com")}},
		{"tag_name": "v1.15.0-beta.1", "prerelease": true, "assets": []any{asset(preName, "https://github.com")}},
		{"tag_name": "v1.13.9", "assets": []any{asset(name, "https://github.com")}},
		{"tag_name": "v1.14.5", "draft": true, "assets": []any{asset(name, "https://github.com")}},
		{"tag_name": "v1.14.4", "assets": []any{asset(name, "https://evil.invalid")}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("per_page") != "10" || r.Header.Get("User-Agent") == "" {
			t.Error("missing metadata request parameters")
		}
		json.NewEncoder(w).Encode(entries)
	}))
	defer server.Close()
	list, err := releases(context.Background(), false, server.Client(), server.URL)
	if err != nil || len(list) != 1 || list[0].Version != "1.14.4" || list[0].Digest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("stable releases: %+v %v", list, err)
	}
	list, err = releases(context.Background(), true, server.Client(), server.URL)
	if err != nil || len(list) != 2 || !list[1].Prerelease {
		t.Fatalf("prereleases: %+v %v", list, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = releases(ctx, false, server.Client(), server.URL); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel metadata: %v", err)
	}
}
func TestInstallVerifiedLocalHelperAndNoOverwrite(t *testing.T) {
	binary := helperBinary(t)
	exe, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := assetName("1.14.4")
	rootName := strings.TrimSuffix(strings.TrimSuffix(name, ".tar.gz"), ".zip")
	executable := "sing-box"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	data := makeArchive(t, name, []archiveEntry{{name: rootName + "/" + executable, data: exe}, {name: rootName + "/LICENSE", data: []byte("test helper fixture")}})
	release := fixtureRelease(t, data)
	client := localAssetClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Write(data)
	})
	root := t.TempDir()
	var last int64
	var calls int
	installed, err := install(context.Background(), root, release, func(done, total int64) {
		if done < last || total != int64(len(data)) {
			t.Errorf("invalid progress %d/%d", done, total)
		}
		last = done
		calls++
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Version != "1.14.4" || calls < 2 || last != int64(len(data)) {
		t.Fatalf("installation: %+v calls=%d done=%d", installed, calls, last)
	}
	entries, err := os.ReadDir(filepath.Dir(installed.Path))
	if err != nil || len(entries) != 1 || entries[0].Name() != executable {
		t.Fatalf("extracted unexpected files: %v %v", entries, err)
	}
	list, err := InstalledVersions(root)
	if err != nil || len(list) != 1 || list[0] != installed {
		t.Fatalf("installed versions: %+v %v", list, err)
	}
	if _, err = install(context.Background(), root, release, nil, client); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("overwrote existing version: %v", err)
	}
	assertNoStaging(t, root)
	// A valid archive hash is not enough: the extracted binary must report the
	// exact selected release version before its directory becomes visible.
	t.Setenv("BOXPILOT_HELPER_VERSION", "1.14.5")
	mismatchRoot := t.TempDir()
	if _, err = install(context.Background(), mismatchRoot, release, nil, client); err == nil || !strings.Contains(err.Error(), "differs from release") {
		t.Fatalf("version mismatch accepted: %v", err)
	}
	assertNoStaging(t, mismatchRoot)
	mismatchVersions, err := InstalledVersions(mismatchRoot)
	if err != nil || len(mismatchVersions) != 0 {
		t.Fatal("failed installation became visible")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(installed.Path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("executable permissions: %v %v", info, err)
		}
	}
}
func assertNoStaging(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "versions"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("leftover install artifact %s", e.Name())
		}
	}
}
func TestInstallRejectsDigestSizeSourceAndCancellation(t *testing.T) {
	data := []byte("not an executable archive")
	good := fixtureRelease(t, data)
	client := localAssetClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write(data) })
	for _, tc := range []struct {
		name   string
		mutate func(*Release)
	}{
		{"missing digest", func(r *Release) { r.Digest = "" }},
		{"bad digest", func(r *Release) { r.Digest = "sha256:" + strings.Repeat("0", 64) }},
		{"wrong size", func(r *Release) { r.Size++ }},
		{"too large", func(r *Release) { r.Size = maxDownloadSize + 1 }},
		{"untrusted source", func(r *Release) { r.DownloadURL = "https://evil.invalid/core" }},
		{"insecure source", func(r *Release) { r.DownloadURL = strings.Replace(r.DownloadURL, "https:", "http:", 1) }},
		{"asset mismatch", func(r *Release) { r.AssetName = "wrong.zip" }},
		{"old version", func(r *Release) { r.Version = "1.13.0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			r := good
			tc.mutate(&r)
			if _, err := install(context.Background(), root, r, nil, client); err == nil {
				t.Fatal("accepted invalid release")
			}
			assertNoStaging(t, root)
		})
	}
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := install(ctx, root, good, nil, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled install: %v", err)
	}
	assertNoStaging(t, root)
	root = t.TempDir()
	ctx, cancel = context.WithCancel(context.Background())
	if _, err := install(ctx, root, good, func(int64, int64) { cancel() }, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled progress: %v", err)
	}
	assertNoStaging(t, root)
}
func TestDownloadChunkedLimitsAndHTTPErrors(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 100)
	r := fixtureRelease(t, data)
	for _, mode := range []string{"oversize", "short", "http-error"} {
		t.Run(mode, func(t *testing.T) {
			client := localAssetClient(t, func(w http.ResponseWriter, req *http.Request) {
				if mode == "http-error" {
					http.Error(w, "no", http.StatusForbidden)
					return
				}
				w.(http.Flusher).Flush()
				if mode == "short" {
					w.Write(data[:50])
				} else {
					w.Write(append(data, 'x'))
				}
			})
			root := t.TempDir()
			if _, err := install(context.Background(), root, r, nil, client); err == nil {
				t.Fatal("accepted invalid download")
			}
			assertNoStaging(t, root)
		})
	}
}
func TestArchiveSafety(t *testing.T) {
	for _, ext := range []string{".tar.gz", ".zip"} {
		t.Run(ext, func(t *testing.T) {
			asset := "sing-box-1.14.4-test" + ext
			root := "sing-box-1.14.4-test"
			exe := "sing-box"
			if ext == ".zip" {
				exe += ".exe"
			}
			valid := archiveEntry{name: root + "/" + exe, data: []byte("binary")}
			cases := []struct {
				name    string
				entries []archiveEntry
			}{
				{"valid", []archiveEntry{valid, {name: root + "/README", data: []byte("ignored")}}},
				{"traversal", []archiveEntry{valid, {name: "../escape", data: []byte("x")}}},
				{"absolute", []archiveEntry{{name: "/escape", data: []byte("x")}, valid}},
				{"backslash", []archiveEntry{valid, {name: root + "\\escape", data: []byte("x")}}},
				{"drive", []archiveEntry{valid, {name: "C:/escape", data: []byte("x")}}},
				{"duplicate", []archiveEntry{valid, valid}},
				{"wrong root", []archiveEntry{{name: "another/" + exe, data: []byte("x")}}},
				{"missing exe", []archiveEntry{{name: root + "/README", data: []byte("x")}}},
				{"path alias", []archiveEntry{valid, {name: root + "/./x", data: []byte("x")}}},
				{"file parent", []archiveEntry{{name: root, data: []byte("x")}, valid}},
			}
			if ext == ".tar.gz" {
				cases = append(cases, struct {
					name    string
					entries []archiveEntry
				}{"symlink", []archiveEntry{valid, {name: root + "/link", kind: tar.TypeSymlink}}}, struct {
					name    string
					entries []archiveEntry
				}{"hardlink", []archiveEntry{valid, {name: root + "/link", kind: tar.TypeLink}}}, struct {
					name    string
					entries []archiveEntry
				}{"special", []archiveEntry{valid, {name: root + "/fifo", kind: tar.TypeFifo}}}, struct {
					name    string
					entries []archiveEntry
				}{"expanded size", []archiveEntry{{name: root + "/" + exe, size: maxExtractSize + 1}}})
			} else {
				cases = append(cases, struct {
					name    string
					entries []archiveEntry
				}{"symlink", []archiveEntry{valid, {name: root + "/link", mode: os.ModeSymlink | 0777, data: []byte("target")}}})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					data := makeArchive(t, asset, tc.entries)
					downloadDir := t.TempDir()
					archive := filepath.Join(downloadDir, "archive")
					os.WriteFile(archive, data, 0600)
					staging := t.TempDir()
					dest := filepath.Join(staging, exe)
					err := extractArchive(context.Background(), archive, asset, dest)
					if tc.name == "valid" {
						if err != nil {
							t.Fatal(err)
						}
						got, err := os.ReadFile(dest)
						if err != nil || string(got) != "binary" {
							t.Fatalf("binary: %q %v", got, err)
						}
						files, _ := os.ReadDir(staging)
						if len(files) != 1 {
							t.Fatal("extracted extra files")
						}
					} else if err == nil {
						t.Fatal("accepted unsafe archive")
					}
				})
			}
		})
	}
}
func TestArchiveCancellationEntryCountAndGzipChecksum(t *testing.T) {
	asset := "sing-box-1.14.4-test.tar.gz"
	valid := archiveEntry{name: "sing-box-1.14.4-test/sing-box", data: []byte("binary")}
	data := makeArchive(t, asset, []archiveEntry{valid})
	archive := filepath.Join(t.TempDir(), "archive")
	os.WriteFile(archive, data, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := extractArchive(ctx, archive, asset, filepath.Join(t.TempDir(), "exe")); !errors.Is(err, context.Canceled) {
		t.Fatalf("archive canceled: %v", err)
	}
	data[len(data)-5] ^= 0xff
	os.WriteFile(archive, data, 0600)
	if err := extractArchive(context.Background(), archive, asset, filepath.Join(t.TempDir(), "exe")); err == nil {
		t.Fatal("ignored gzip checksum")
	}
	g := archiveGuard{seen: make(map[string]bool)}
	if err := g.entry("root", maxExtractSize, false); err != nil {
		t.Fatal(err)
	}
	if err := g.entry("other", 1, false); err == nil {
		t.Fatal("ignored expanded total")
	}
	g = archiveGuard{seen: make(map[string]bool), count: maxArchiveEntries}
	if err := g.entry("root", 0, true); err == nil {
		t.Fatal("ignored entry limit")
	}
}
func TestDistributionRedirectRestrictions(t *testing.T) {
	client := releaseClient()
	req, _ := http.NewRequest("GET", "https://evil.invalid/asset", nil)
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("allowed untrusted redirect")
	}
	req, _ = http.NewRequest("GET", "http://release-assets.githubusercontent.com/a", nil)
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("allowed plaintext redirect")
	}
	req, _ = http.NewRequest("GET", "https://release-assets.githubusercontent.com/a", nil)
	if err := client.CheckRedirect(req, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, make([]*http.Request, 8)); err == nil {
		t.Fatal("unbounded redirects")
	}
	for _, bad := range []string{"http://github.com/SagerNet/sing-box/releases/download/v1/x", "https://github.com.evil/SagerNet/sing-box/releases/download/v1/x", "https://user@github.com/SagerNet/sing-box/releases/download/v1/x", "https://github.com/other/sing-box/releases/download/v1/x"} {
		if officialDownload(bad) {
			t.Errorf("accepted source %s", bad)
		}
	}
}
