//go:build !windows

package core

import (
	"os"
	"path/filepath"
)

func prepareRunConfig(root, _ string, _ []byte) (string, string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if err = privateDir(root); err != nil {
		return "", "", err
	}
	runtimeDir := filepath.Join(root, "runtime")
	if err = privateDir(runtimeDir); err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp(runtimeDir, "run-*")
	if err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, "config.json"), nil
}

func cleanupRunDirectory(dir string) error {
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}
