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
	"time"

	"github.com/tailscale/hujson"
)

// 在不可信 JSONC 进入 hujson 的递归解析器前限制嵌套深度。
const maxConfigDepth = 256

var profileMu sync.Mutex
var profileIDPattern = regexp.MustCompile(`^[a-f0-9]{48}$`)

func DefaultConfig() string {
	return "{\n  \"log\": {\"level\": \"info\"},\n  \"outbounds\": [{\"type\": \"direct\", \"tag\": \"direct\"}]\n}\n"
}
func ConfigHasTUNInbound(content []byte) (bool, error) {
	config, err := parseConfig(content)
	if err != nil {
		return false, err
	}
	var inbounds []struct {
		Type string `json:"type"`
	}
	if raw, ok := config["inbounds"]; ok {
		if strings.TrimSpace(string(raw)) == "null" {
			return false, errors.New("inbounds must be an array")
		}
		if err := json.Unmarshal(raw, &inbounds); err != nil {
			return false, fmt.Errorf("inbounds: %w", err)
		}
	}
	for _, inbound := range inbounds {
		if inbound.Type == "tun" {
			return true, nil
		}
	}
	return false, nil
}

func parseConfig(content []byte) (map[string]json.RawMessage, error) {
	if int64(len(content)) > maxConfigSize {
		return nil, errors.New("configuration too large")
	}
	if err := checkConfigDepth(content); err != nil {
		return nil, err
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

// checkConfigDepth 非递归预扫描嵌套深度，不替代 JSON 语法校验。
// 字符串、转义字符及注释中的括号不计入深度，后续仍由 hujson 检查语法。
func checkConfigDepth(content []byte) error {
	const (
		normal = iota
		quoted
		lineComment
		blockComment
	)
	state, depth := normal, 0
	for i := 0; i < len(content); i++ {
		c := content[i]
		switch state {
		case quoted:
			if c == '\\' {
				i++ // 跳过被转义的字节，包括引号和反斜杠。
			} else if c == '"' {
				state = normal
			}
		case lineComment:
			if c == '\n' {
				state = normal
			}
		case blockComment:
			if c == '*' && i+1 < len(content) && content[i+1] == '/' {
				i++
				state = normal
			}
		default:
			switch c {
			case '"':
				state = quoted
			case '/':
				if i+1 < len(content) {
					switch content[i+1] {
					case '/':
						state = lineComment
						i++
					case '*':
						state = blockComment
						i++
					}
				}
			case '{', '[':
				depth++
				if depth > maxConfigDepth {
					return fmt.Errorf("invalid JSONC: nesting depth exceeds %d", maxConfigDepth)
				}
			case '}', ']':
				if depth == 0 {
					return errors.New("invalid JSONC: unexpected closing delimiter")
				}
				depth--
			}
		}
	}
	return nil
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
		p := Profile{ID: e.Name(), Name: display, Path: filepath.Join(base, e.Name(), "config.json"), Type: "local"}
		metadataPath := filepath.Join(base, e.Name(), "profile.json")
		if metadata, readErr := readLimited(metadataPath, 16<<10); readErr == nil {
			var saved Profile
			if err = json.Unmarshal(metadata, &saved); err != nil {
				return nil, fmt.Errorf("profile metadata %s: %w", e.Name(), err)
			}
			if saved.Type != "local" && saved.Type != "remote" {
				return nil, fmt.Errorf("invalid profile type %q", saved.Type)
			}
			p.Type = saved.Type
			p.RemoteURL = saved.RemoteURL
			p.AutoUpdate = saved.AutoUpdate
			p.AutoUpdateInterval = saved.AutoUpdateInterval
			p.LastUpdated = saved.LastUpdated
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		if p.Type == "remote" {
			if _, err = ValidateRemoteURL(p.RemoteURL); err != nil {
				return nil, fmt.Errorf("invalid remote profile URL: %w", err)
			}
		}
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
	return SaveProfileWithMetadata(root, Profile{ID: id, Type: "local"}, name, content)
}

func SaveProfileWithMetadata(root string, metadata Profile, name, content string) (Profile, error) {
	if _, err := parseConfig([]byte(content)); err != nil {
		return Profile{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 1024 {
		return Profile{}, errors.New("profile name must contain 1–1024 bytes")
	}
	if metadata.Type == "" {
		metadata.Type = "local"
	}
	if metadata.Type != "local" && metadata.Type != "remote" {
		return Profile{}, errors.New("invalid profile type")
	}
	if metadata.Type == "remote" {
		if _, err := ValidateRemoteURL(metadata.RemoteURL); err != nil {
			return Profile{}, err
		}
		if metadata.AutoUpdateInterval <= 0 {
			metadata.AutoUpdateInterval = 60
		}
	} else {
		metadata.RemoteURL = ""
		metadata.AutoUpdate = false
		metadata.AutoUpdateInterval = 0
		metadata.LastUpdated = time.Time{}
	}
	profileMu.Lock()
	defer profileMu.Unlock()
	base, err := profileBase(root)
	if err != nil {
		return Profile{}, err
	}
	id := metadata.ID
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
	metadata.ID = id
	metadata.Name = name
	metadata.Path = path
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return Profile{}, err
	}
	if err = atomicWrite(filepath.Join(dir, "profile.json"), metadataBytes); err != nil {
		return Profile{}, err
	}
	success = true
	return metadata, nil
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
	// 运行副本不需要缩进；深层数组经缩进可能把小输入放大数百倍。
	derived, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if int64(len(derived)) > maxConfigSize {
		return nil, errors.New("derived configuration too large")
	}
	return derived, nil
}
