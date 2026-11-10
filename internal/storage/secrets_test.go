package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestSecretSessionNoDiskAndScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	if err := SaveSecret(root, "id", "session-sensitive", RememberSession); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session wrote to disk: %v", err)
	}
	value, err := LoadSecret(root, "id", RememberSession)
	if err != nil || value != "session-sensitive" {
		t.Fatalf("session: %q, %v", value, err)
	}
	s := Defaults()
	s.Servers = []Server{{ID: "id", Name: "session", Remember: RememberSession}}
	if err := Save(root, s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, SettingsFile))
	if err != nil || strings.Contains(string(data), "session-sensitive") || strings.Contains(strings.ToLower(string(data)), "secret") {
		t.Fatalf("session credential leaked into JSON: %s %v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != SettingsFile {
		t.Fatalf("session mode wrote an unexpected file: %v %v", entries, err)
	}
	for _, other := range [][2]string{{root, "other"}, {root + "-other", "id"}} {
		if _, err := LoadSecret(other[0], other[1], RememberSession); !errors.Is(err, ErrSecretNotFound) {
			t.Fatalf("credential scope leaked: %v", err)
		}
	}
	if err := SaveSecret(root, "empty", "", RememberSession); err != nil {
		t.Fatal(err)
	}
	if value, err := LoadSecret(root, "empty", RememberSession); err != nil || value != "" {
		t.Fatalf("empty secret is different from missing: %q %v", value, err)
	}
	for i := 0; i < 2; i++ {
		if err := DeleteSecret(root, "id", RememberSession); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadSecret(root, "id", RememberSession); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal(err)
	}
}

func TestSecretFileSeparatePrivateAndNoJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	id, value := "../../arbitrary/ID", "file-sensitive-你好\n\x00"
	if err := SaveSecret(root, id, value, RememberFile); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSecret(root, id, RememberFile); err != nil || got != value {
		t.Fatalf("file secret: %q, %v", got, err)
	}
	s := Defaults()
	s.Servers = []Server{{ID: id, Name: "name", URL: "https://example.invalid", Remember: RememberFile}}
	if err := Save(root, s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, SettingsFile))
	if err != nil || strings.Contains(string(data), "sensitive") || strings.Contains(strings.ToLower(string(data)), "secret") {
		t.Fatalf("settings contain a credential: %s %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "secrets"))
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".secret") {
		t.Fatalf("unexpected secret files: %v, %v", entries, err)
	}
	if runtime.GOOS != "windows" {
		checkPerm(t, root, 0o700)
		checkPerm(t, filepath.Join(root, "secrets"), 0o700)
		checkPerm(t, filepath.Join(root, "secrets", entries[0].Name()), 0o600)
	}
	if err := SaveSecret(root, id, "replaced", RememberFile); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSecret(root, id, RememberFile); err != nil || got != "replaced" {
		t.Fatalf("replace: %q %v", got, err)
	}
	for i := 0; i < 2; i++ {
		if err := DeleteSecret(root, id, RememberFile); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadSecret(root, id, RememberFile); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := DeleteSecret(filepath.Join(t.TempDir(), "absent"), id, RememberFile); err != nil {
		t.Fatal(err)
	}
}

func TestSecretModeValidationAndIsolation(t *testing.T) {
	root := t.TempDir()
	for _, input := range [][3]string{{root, "id", ""}, {root, "id", "fallback"}, {root, "", RememberSession}, {root, "a\x00b", RememberFile}, {"", "id", RememberFile}} {
		if err := SaveSecret(input[0], input[1], "sensitive", input[2]); err == nil {
			t.Fatal("invalid save accepted")
		}
		if _, err := LoadSecret(input[0], input[1], input[2]); err == nil {
			t.Fatal("invalid load accepted")
		}
		if err := DeleteSecret(input[0], input[1], input[2]); err == nil {
			t.Fatal("invalid delete accepted")
		}
	}
	if err := SaveSecret(root, "id", "file", RememberFile); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecret(root, "id", RememberSession); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("file mode silently read into session")
	}
	if err := SaveSecret(root, "id", "session", RememberSession); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSecret(root, "id", RememberSession); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSecret(root, "id", RememberFile); err != nil || got != "file" {
		t.Fatalf("session delete affected file: %q %v", got, err)
	}
}

type unsupportedKeychain struct{}

func (unsupportedKeychain) Set(string, []byte) error   { return errors.ErrUnsupported }
func (unsupportedKeychain) Get(string) ([]byte, error) { return nil, errors.ErrUnsupported }
func (unsupportedKeychain) Delete(string) error        { return errors.ErrUnsupported }

// This tests error propagation, not the real OS store: headless unit tests must
// never unlock/prompt for or modify the user's actual keychain.
func TestKeychainUnsupportedNeverFallsBack(t *testing.T) {
	old := keychain
	keychain = unsupportedKeychain{}
	t.Cleanup(func() { keychain = old })
	root := filepath.Join(t.TempDir(), "absent")
	if err := SaveSecret(root, "id", "sensitive", RememberKeychain); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("save: %v", err)
	}
	if _, err := LoadSecret(root, "id", RememberKeychain); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("load: %v", err)
	}
	if err := DeleteSecret(root, "id", RememberKeychain); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsupported keychain silently created a file store")
	}
	if _, err := LoadSecret(root, "id", RememberSession); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("unsupported keychain silently populated session store")
	}
}

func TestSecretRejectSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveSecret(root, "id", "first", RememberFile); err != nil {
		t.Fatal(err)
	}
	_, key, _ := secretKey(root, "id", RememberFile)
	path := filepath.Join(root, "secrets", key+".secret")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := SaveSecret(root, "id", "overwrite", RememberFile); err == nil {
		t.Fatal("symlink written")
	}
	if _, err := LoadSecret(root, "id", RememberFile); err == nil {
		t.Fatal("symlink read")
	}
	if err := DeleteSecret(root, "id", RememberFile); err == nil {
		t.Fatal("symlink removed")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "untouched" {
		t.Fatal("external file modified")
	}
	// Also reject a symlinked secrets directory, not only the final file.
	if err := os.Rename(filepath.Join(root, "secrets"), filepath.Join(root, "old-secrets")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "old-secrets"), filepath.Join(root, "secrets")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecret(root, "id", RememberFile); err == nil {
		t.Fatal("symlinked directory read")
	}
	if err := SaveSecret(root, "id", "overwrite", RememberFile); err == nil {
		t.Fatal("symlinked directory written")
	}
	if err := DeleteSecret(root, "id", RememberFile); err == nil {
		t.Fatal("symlinked directory traversed by delete")
	}
}

func TestSessionConcurrentAccess(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 48)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			if err := SaveSecret(root, id, id, RememberSession); err != nil {
				errs <- err
			}
			if got, err := LoadSecret(root, id, RememberSession); err != nil || got != id {
				errs <- fmt.Errorf("concurrent session %q: %v", got, err)
			}
			if err := DeleteSecret(root, id, RememberSession); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
