package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/tailscale/hujson"
)

var profileMu sync.Mutex
var profileIDPattern = regexp.MustCompile(`^[a-f0-9]{48}$`)

func DefaultConfig() string {
	return "{\n  \"log\": {\"level\": \"info\"},\n  \"outbounds\": [{\"type\": \"direct\", \"tag\": \"direct\"}]\n}\n"
}
func parseConfig(content []byte) (map[string]json.RawMessage, error) {
	if int64(len(content)) > maxConfigSize {
		return nil, errors.New("configuration too large")
	}
	standard, err := hujson.Standardize(content)
	if err != nil {
		return nil, fmt.Errorf("invalid JSONC: %w", err)
	}
	var config map[string]json.RawMessage
	if err = json.Unmarshal(standard, &config); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, errors.New("configuration must be an object")
	}
	return config, nil
}
func profileBase(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err = privateDir(root); err != nil {
		return "", err
	}
	base := filepath.Join(root, "profiles")
	if err = privateDir(base); err != nil {
		return "", err
	}
	return base, nil
}
func Profiles(root string) ([]Profile, error) {
	profileMu.Lock()
	defer profileMu.Unlock()
	base, err := profileBase(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	result := []Profile{}
	for _, e := range entries {
		if !profileIDPattern.MatchString(e.Name()) {
			continue
		}
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe profile directory")
		}
		name, err := readLimited(filepath.Join(base, e.Name(), "name.json"), 8<<10)
		if err != nil {
			return nil, err
		}
		var display string
		if err = json.Unmarshal(name, &display); err != nil {
			return nil, err
		}
		p := Profile{ID: e.Name(), Name: display, Path: filepath.Join(base, e.Name(), "config.json")}
		if _, err = readLimited(p.Path, maxConfigSize); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
func SaveProfile(root, id, name, content string) (Profile, error) {
	if _, err := parseConfig([]byte(content)); err != nil {
		return Profile{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 1024 {
		return Profile{}, errors.New("profile name must contain 1–1024 bytes")
	}
	profileMu.Lock()
	defer profileMu.Unlock()
	base, err := profileBase(root)
	if err != nil {
		return Profile{}, err
	}
	fresh := id == ""
	if fresh {
		id, err = randomID()
		if err != nil {
			return Profile{}, err
		}
	}
	if !profileIDPattern.MatchString(id) {
		return Profile{}, errors.New("invalid profile ID")
	}
	dir := filepath.Join(base, id)
	if fresh {
		if err = os.Mkdir(dir, 0700); err != nil {
			return Profile{}, err
		}
	} else {
		info, err := os.Lstat(dir)
		if err != nil {
			return Profile{}, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Profile{}, errors.New("unsafe profile directory")
		}
	}
	success := false
	defer func() {
		if fresh && !success {
			os.RemoveAll(dir)
		}
	}()
	nameBytes, _ := json.Marshal(name)
	if err = atomicWrite(filepath.Join(dir, "name.json"), nameBytes); err != nil {
		return Profile{}, err
	}
	path := filepath.Join(dir, "config.json")
	if err = atomicWrite(path, []byte(content)); err != nil {
		return Profile{}, err
	}
	success = true
	return Profile{ID: id, Name: name, Path: path}, nil
}
func ReadProfile(p Profile) (string, error) {
	b, err := readLimited(p.Path, maxConfigSize)
	return string(b), err
}
func DeleteProfile(root, id string) error {
	if !profileIDPattern.MatchString(id) {
		return errors.New("invalid profile ID")
	}
	profileMu.Lock()
	defer profileMu.Unlock()
	base, err := profileBase(root)
	if err != nil {
		return err
	}
	path := filepath.Join(base, id)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe profile directory")
	}
	return os.RemoveAll(path)
}

// deriveConfig never rewrites the source. Only an explicitly approved API service
// is removed; every other service and unknown top-level field is preserved.
func deriveConfig(content []byte, port int, secret string, allowAPIReplace bool) ([]byte, error) {
	config, err := parseConfig(content)
	if err != nil {
		return nil, err
	}
	var services []json.RawMessage
	if raw, ok := config["services"]; ok {
		if strings.TrimSpace(string(raw)) == "null" {
			return nil, errors.New("services must be an array")
		}
		if err = json.Unmarshal(raw, &services); err != nil {
			return nil, fmt.Errorf("services: %w", err)
		}
	}
	kept := make([]json.RawMessage, 0, len(services)+1)
	for _, raw := range services {
		var service struct {
			Type string `json:"type"`
		}
		if err = json.Unmarshal(raw, &service); err != nil {
			return nil, err
		}
		if service.Type == "api" {
			if !allowAPIReplace {
				return nil, ErrAPIConflict
			}
			continue
		}
		kept = append(kept, raw)
	}
	api, err := json.Marshal(map[string]any{"type": "api", "listen": "127.0.0.1", "listen_port": port, "secret": secret, "dashboard": false})
	if err != nil {
		return nil, err
	}
	kept = append(kept, api)
	config["services"], err = json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(config, "", "  ")
}
