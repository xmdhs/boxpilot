//go:build windows

package core

import (
	"path/filepath"
)

func prepareRunConfig(_ string, profilePath string, _ []byte) (string, string, error) {
	dir, err := filepath.Abs(filepath.Dir(profilePath))
	if err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, "boxpilot-derived-config.json"), nil
}

func cleanupRunDirectory(string) error { return nil }
