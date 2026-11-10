package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLoadMissingAndRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	got, err := Load(root)
	if err != nil || !reflect.DeepEqual(got, Defaults()) {
		t.Fatalf("missing: %+v, %v", got, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load created missing root: %v", err)
	}
	s := Defaults()
	s.Theme, s.Accent, s.Language = "dark", "violet", "zh-CN"
	s.TrayEnabled, s.MinimizeToTray, s.CloseToTray, s.OpenAtLogin = false, false, false, true
	s.Servers = []Server{{ID: GenerateID(), Name: "server", URL: "https://example.invalid", Remember: RememberSession}}
	s.ActiveID = s.Servers[0].ID
	s.CorePath, s.ProfileID = "/custom/core", "profile"
	s.Cards, s.DisabledCards = []string{"traffic", "connections"}, []string{"memory"}
	s.ConnectionFilter, s.ConnectionSort = "active", "upload"
	if err := Save(root, s); err != nil {
		t.Fatal(err)
	}
	got, err = Load(root)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip: %+v, %v; want %+v", got, err, s)
	}
	data, err := os.ReadFile(filepath.Join(root, SettingsFile))
	if err != nil || !json.Valid(data) {
		t.Fatalf("invalid JSON: %s, %v", data, err)
	}
}

func TestLoadCorruptDoesNotOverwrite(t *testing.T) {
	for _, text := range []string{
		`{`, `null`, `[]`, `"settings"`, `{ "schemaVersion": "1" }`,
		`{"schemaVersion": 99}`, `{"schemaVersion": -1}`, `{"schemaVersion":null}`, `{} {}`,
		`{"trayEnabled":"yes"}`, `{"servers":[{"id":"a","remember":"magic"}]}`,
	} {
		t.Run(text, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, SettingsFile)
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatal("corrupt or unsupported settings accepted")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != text {
				t.Fatalf("Load changed corrupt data: %q, %v", data, err)
			}
		})
	}
}

func TestSchemaDefaultsAndValidation(t *testing.T) {
	root := t.TempDir()
	for _, text := range []string{`{}`, `{"schemaVersion":0}`, `{"schemaVersion":1,"theme":"dark","trayEnabled":false}`} {
		path := filepath.Join(root, SettingsFile)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(root)
		if err != nil || s.SchemaVersion != SchemaVersion || s.Accent != Defaults().Accent {
			t.Fatalf("defaults/migration: %+v, %v", s, err)
		}
		if strings.Contains(text, "false") && s.TrayEnabled {
			t.Fatal("explicit false was replaced by default")
		}
		data, _ := os.ReadFile(path)
		if string(data) != text {
			t.Fatal("migration must be in memory only")
		}
	}
	valid := Defaults()
	if err := Save(root, valid); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, SettingsFile))
	bad := []Settings{valid, valid, valid, valid}
	bad[0].SchemaVersion = SchemaVersion + 1
	bad[1].Servers = []Server{{ID: "", Remember: RememberSession}}
	bad[2].Servers = []Server{{ID: "a", Remember: ""}}
	bad[3].Servers = []Server{{ID: "a", Remember: RememberFile}, {ID: "a", Remember: RememberSession}}
	for _, s := range bad {
		if err := Save(root, s); err == nil {
			t.Fatal("invalid settings accepted")
		}
		after, _ := os.ReadFile(filepath.Join(root, SettingsFile))
		if !bytes.Equal(before, after) {
			t.Fatal("failed Save replaced valid settings")
		}
	}
	if _, err := Load(""); err == nil {
		t.Fatal("empty root accepted")
	}
}

func TestSavePrivatePermissionsAndAtomicReplace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, SettingsFile)
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := Save(root, Defaults()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		checkPerm(t, root, 0o700)
		checkPerm(t, path, 0o600)
		// An open descriptor must still reference the old inode: an in-place
		// truncate/write implementation would fail this assertion.
		var b [3]byte
		if _, err := old.ReadAt(b[:], 0); err != nil || string(b[:]) != "old" {
			t.Fatalf("Save did not replace atomically: %q %v", b, err)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != SettingsFile {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestSaveFailureAndSymlinks(t *testing.T) {
	root := t.TempDir()
	// A non-regular target must not be replaced or leave temp files.
	if err := os.Mkdir(filepath.Join(root, SettingsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, Defaults()); err == nil {
		t.Fatal("directory target accepted")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatal("temporary file leaked on failure")
	}
	if runtime.GOOS == "windows" {
		return // symlink creation requires privileges on Windows
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, SettingsFile)); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, Defaults()); err == nil {
		t.Fatal("symlink target accepted")
	}
	if _, err := Load(root); err == nil {
		t.Fatal("symlink settings read")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "untouched" {
		t.Fatal("symlink target modified")
	}
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("symlink root read")
	}
	if err := Save(link, Defaults()); err == nil {
		t.Fatal("symlink root written")
	}
}

func TestConcurrentSavesAndAtomicReaders(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, Defaults()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				s := Defaults()
				s.ProfileID = fmt.Sprintf("%d-%d", i, j)
				s.Cards = []string{strings.Repeat("card", 1000)}
				if err := Save(root, s); err != nil {
					errs <- err
					return
				}
				// Bypass diskMu to check actual atomicity, not just Load's lock.
				data, err := os.ReadFile(filepath.Join(root, SettingsFile))
				if err != nil {
					errs <- err
				} else if !json.Valid(data) {
					errs <- errors.New("external reader observed partial JSON")
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if _, err := Load(root); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := GenerateID()
		if len(id) != 36 || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) || seen[id] {
			t.Fatalf("invalid or duplicate UUID: %q", id)
		}
		seen[id] = true
	}
}

func checkPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s permissions %o, want %o", path, info.Mode().Perm(), want)
	}
}
