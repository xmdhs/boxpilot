package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseMetadataErrorsAndPagination(t *testing.T) {
	for _, mode := range []string{"malformed", "http-error", "oversize", "pagination"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			name, _ := assetName("1.14.4")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch mode {
				case "http-error":
					http.Error(w, "rate limited", 403)
				case "malformed":
					w.Write([]byte("not JSON"))
				case "oversize":
					w.Write([]byte(strings.Repeat("x", (16<<20)+1)))
				case "pagination":
					if calls == 1 {
						json.NewEncoder(w).Encode(make([]any, 100))
						return
					}
					json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v1.14.4", "assets": []map[string]any{{"name": name, "browser_download_url": "https://github.com/SagerNet/sing-box/releases/download/v1.14.4/" + name}}}})
				}
			}))
			defer server.Close()
			list, err := releases(context.Background(), false, server.Client(), server.URL)
			if mode == "pagination" {
				if err != nil || len(list) != 1 || calls != 2 {
					t.Fatalf("pagination: %+v %v calls=%d", list, err, calls)
				}
			} else if err == nil {
				t.Fatal("metadata failure ignored")
			}
		})
	}
}
func TestInstalledVersionsRejectsSymlinkAndIgnoresPartialInstalls(t *testing.T) {
	root := t.TempDir()
	versions := filepath.Join(root, "versions")
	if err := os.Mkdir(versions, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".download-partial", ".install-partial", "1.13.0", "1.14.4"} {
		if err := os.Mkdir(filepath.Join(versions, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	list, err := InstalledVersions(root)
	if err != nil || len(list) != 0 {
		t.Fatalf("partial installation visible: %+v %v", list, err)
	}
	if runtime.GOOS != "windows" {
		root = t.TempDir()
		if err = os.Symlink(versions, filepath.Join(root, "versions")); err != nil {
			t.Fatal(err)
		}
		if _, err = InstalledVersions(root); err == nil {
			t.Fatal("followed versions symlink")
		}
	}
}
func TestRejectsConcatenatedTarArchive(t *testing.T) {
	asset := "sing-box-1.14.4-test.tar.gz"
	first := makeArchive(t, asset, []archiveEntry{{name: "sing-box-1.14.4-test/sing-box", data: []byte("binary")}})
	second := makeArchive(t, asset, []archiveEntry{{name: "../escape", data: []byte("hidden")}})
	archive := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(archive, append(first, second...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(context.Background(), archive, asset, filepath.Join(t.TempDir(), "exe")); err == nil {
		t.Fatal("accepted hidden concatenated archive")
	}
}
