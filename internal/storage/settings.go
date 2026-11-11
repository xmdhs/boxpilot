// Package storage persists settings and keeps credentials in a separate store.
package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const SchemaVersion = 1
const SettingsFile = "settings.json"

// Server deliberately has no Secret field. Remember is session, keychain or file.
type Server struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Remember string `json:"remember"`
}

type Settings struct {
	SchemaVersion    int      `json:"schemaVersion"`
	Theme            string   `json:"theme"`
	Accent           string   `json:"accent"`
	Language         string   `json:"language"`
	TrayEnabled      bool     `json:"trayEnabled"`
	MinimizeToTray   bool     `json:"minimizeToTray"`
	CloseToTray      bool     `json:"closeToTray"`
	OpenAtLogin      bool     `json:"openAtLogin"`
	ElevateCore      bool     `json:"elevateCore"`
	Servers          []Server `json:"servers"`
	ActiveID         string   `json:"activeID"`
	CorePath         string   `json:"corePath"`
	ProfileID        string   `json:"profileID"`
	Cards            []string `json:"cards"`
	DisabledCards    []string `json:"disabledCards"`
	ConnectionFilter string   `json:"connectionFilter"`
	ConnectionSort   string   `json:"connectionSort"`
}

func Defaults() Settings {
	return Settings{
		SchemaVersion: SchemaVersion, Theme: "auto", Accent: "default", Language: "zh-Hans",
		TrayEnabled: true, MinimizeToTray: true, CloseToTray: true,
		Servers: []Server{}, Cards: []string{}, DisabledCards: []string{},
		ConnectionFilter: "active", ConnectionSort: "date",
	}
}

// diskMu serializes settings and explicit file-secret operations in this process.
// Multiple independent processes must not write the same root concurrently.
var diskMu sync.Mutex

// Load returns defaults only for a missing file. Invalid/unsupported data is
// reported without modifying the file. Version 0 (including an absent version)
// is the initial unversioned format and is upgraded in memory only.
func Load(root string) (Settings, error) {
	diskMu.Lock()
	defer diskMu.Unlock()
	root, err := storageRoot(root)
	if err != nil {
		return Settings{}, err
	}
	if err := realDir(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return Settings{}, err
	}
	path := filepath.Join(root, SettingsFile)
	if err := regularFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return Settings{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	// Unmarshalling null into a struct silently succeeds; it is not settings.
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("expected a JSON object")
		}
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	for name, raw := range object {
		if strings.EqualFold(name, "schemaVersion") && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Settings{}, errors.New("settings schema version must be an integer, not null")
		}
	}
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	// A missing version and explicit zero both denote v0.
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	if err := validate(s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Save atomically replaces settings.json using a private, synced temporary file
// in the same directory. It never writes credentials. Save does not silently
// change a schema version: callers should start with Defaults or Load.
func Save(root string, s Settings) error {
	diskMu.Lock()
	defer diskMu.Unlock()
	if err := validate(s); err != nil {
		return err
	}
	root, err := storageRoot(root)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := privateDir(root); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(root, SettingsFile), append(data, '\n'))
}

func validate(s Settings) error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported settings schema %d (supported: %d)", s.SchemaVersion, SchemaVersion)
	}
	seen := make(map[string]bool, len(s.Servers))
	for _, server := range s.Servers {
		if err := checkID(server.ID); err != nil {
			return err
		}
		if seen[server.ID] {
			return fmt.Errorf("duplicate server ID %q", server.ID)
		}
		seen[server.ID] = true
		if !validMode(server.Remember) {
			return fmt.Errorf("invalid remember mode %q", server.Remember)
		}
	}
	return nil
}

func storageRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("storage root must not be empty")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("storage root: %w", err)
	}
	return root, nil
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if err := realDir(path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure storage directory: %w", err)
	}
	return nil
}

func realDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("storage directory is not a real directory: %s", path)
	}
	return nil
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular storage file: %s", path)
	}
	return nil
}

func atomicWrite(path string, data []byte) error {
	if err := regularFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".boxpilot-*")
	if err != nil {
		return fmt.Errorf("create storage temporary file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write storage temporary file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace storage file: %w", err)
	}
	// Directory fsync is not supported by Windows. POSIX permissions above
	// are best-effort on Windows; the profile directory's ACL governs access.
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return fmt.Errorf("sync storage directory: %w", err)
		}
	}
	return nil
}

// GenerateID returns a cryptographically random UUID v4. An unavailable system
// random source is fatal rather than producing a colliding or predictable ID.
func GenerateID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate server ID: %w", err))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
