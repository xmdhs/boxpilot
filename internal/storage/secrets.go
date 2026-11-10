package storage

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
)

const (
	RememberSession  = "session"
	RememberKeychain = "keychain"
	RememberFile     = "file"
)

// ErrSecretNotFound is shared by all three stores. Delete is idempotent.
var ErrSecretNotFound = mygo.ErrSecretNotFound

var session = struct {
	sync.Mutex
	values map[string]string
}{values: make(map[string]string)}

type credentialStore interface {
	Set(string, []byte) error
	Get(string) ([]byte, error)
	Delete(string) error
}

var keychain credentialStore = mygo.Secrets.Service("boxpilot")

func validMode(mode string) bool {
	return mode == RememberSession || mode == RememberKeychain || mode == RememberFile
}

func checkID(id string) error {
	if id == "" || strings.ContainsRune(id, 0) {
		return errors.New("server ID must be nonempty and must not contain NUL")
	}
	return nil
}

func secretKey(root, id, mode string) (string, string, error) {
	if !validMode(mode) {
		return "", "", fmt.Errorf("invalid secret mode %q (expected session, keychain or file)", mode)
	}
	if err := checkID(id); err != nil {
		return "", "", err
	}
	root, err := storageRoot(root)
	if err != nil {
		return "", "", err
	}
	// Hashing keeps arbitrary IDs out of paths and scopes session/keychain
	// credentials to this settings root without exceeding keychain key limits.
	key := fmt.Sprintf("server-%x", sha256.Sum256([]byte(root+"\x00"+id)))
	return root, key, nil
}

// SaveSecret writes to exactly the requested store; there is no fallback. In
// file mode this is explicitly plaintext in a separate 0600 .secret file, never
// settings JSON. Session mode performs no disk I/O. A mode change does not
// delete another store: the caller must explicitly DeleteSecret in the old mode.
// Keychain calls can prompt/block and should be made on a worker goroutine by
// the caller; errors.Is(err, errors.ErrUnsupported) identifies an unavailable
// system store (including Linux without libsecret/Secret Service).
func SaveSecret(root, id, secret, mode string) error {
	root, key, err := secretKey(root, id, mode)
	if err != nil {
		return err
	}
	switch mode {
	case RememberSession:
		session.Lock()
		session.values[key] = secret
		session.Unlock()
		return nil
	case RememberKeychain:
		if err := keychain.Set(key, []byte(secret)); err != nil {
			return fmt.Errorf("save keychain secret: %w", err)
		}
		return nil
	default: // explicitly validated RememberFile
		diskMu.Lock()
		defer diskMu.Unlock()
		if err := privateDir(root); err != nil {
			return err
		}
		dir := filepath.Join(root, "secrets")
		if err := privateDir(dir); err != nil {
			return err
		}
		return atomicWrite(filepath.Join(dir, key+".secret"), []byte(secret))
	}
}

func LoadSecret(root, id, mode string) (string, error) {
	root, key, err := secretKey(root, id, mode)
	if err != nil {
		return "", err
	}
	switch mode {
	case RememberSession:
		session.Lock()
		value, ok := session.values[key]
		session.Unlock()
		if !ok {
			return "", ErrSecretNotFound
		}
		return value, nil
	case RememberKeychain:
		value, err := keychain.Get(key)
		if err != nil {
			return "", fmt.Errorf("load keychain secret: %w", err)
		}
		return string(value), nil
	default:
		diskMu.Lock()
		defer diskMu.Unlock()
		if err := secretDirs(root); err != nil {
			return "", err
		}
		path := filepath.Join(root, "secrets", key+".secret")
		if err := regularFile(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", ErrSecretNotFound
			}
			return "", err
		}
		value, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSecretNotFound
		}
		if err != nil {
			return "", fmt.Errorf("load file secret: %w", err)
		}
		return string(value), nil
	}
}

func secretDirs(root string) error {
	for _, dir := range []string{root, filepath.Join(root, "secrets")} {
		if err := realDir(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrSecretNotFound
			}
			return err
		}
	}
	return nil
}

func DeleteSecret(root, id, mode string) error {
	root, key, err := secretKey(root, id, mode)
	if err != nil {
		return err
	}
	switch mode {
	case RememberSession:
		session.Lock()
		delete(session.values, key)
		session.Unlock()
		return nil
	case RememberKeychain:
		if err := keychain.Delete(key); err != nil && !errors.Is(err, ErrSecretNotFound) {
			return fmt.Errorf("delete keychain secret: %w", err)
		}
		return nil
	default:
		diskMu.Lock()
		defer diskMu.Unlock()
		if err := secretDirs(root); err != nil {
			if errors.Is(err, ErrSecretNotFound) {
				return nil
			}
			return err
		}
		path := filepath.Join(root, "secrets", key+".secret")
		if err := regularFile(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete file secret: %w", err)
		}
		return nil
	}
}
