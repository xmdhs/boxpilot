package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProfilesCRUDAndPreservation(t *testing.T) {
	root := t.TempDir()
	original := "{ // keep my comment\n \"outbounds\": [{\"type\":\"direct\"}], \"custom\": {\"x\":1},\n \"services\": [{\"type\":\"resolved\",\"tag\":\"keep\"}],\n}\n"
	p, err := SaveProfile(root, "", "../../not-a-path", original)
	if err != nil {
		t.Fatal(err)
	}
	if !profileIDPattern.MatchString(p.ID) || !strings.HasPrefix(p.Path, filepath.Join(root, "profiles")+string(filepath.Separator)) {
		t.Fatalf("unsafe profile: %+v", p)
	}
	got, err := ReadProfile(p)
	if err != nil || got != original {
		t.Fatalf("source changed: %q %v", got, err)
	}
	derived, err := deriveConfig([]byte(got), 12345, "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(derived) {
		t.Fatal("derived config is not standard JSON")
	}
	var config map[string]json.RawMessage
	json.Unmarshal(derived, &config)
	var services []map[string]any
	json.Unmarshal(config["services"], &services)
	if len(services) != 2 || services[0]["tag"] != "keep" || services[1]["listen"] != "127.0.0.1" || services[1]["listen_port"] != float64(12345) || services[1]["dashboard"] != false || services[1]["secret"] != "secret" {
		t.Fatalf("wrong services: %s", derived)
	}
	if string(config["custom"]) == "" {
		t.Fatal("unknown field removed")
	}
	list, err := Profiles(root)
	if err != nil || len(list) != 1 || list[0] != p {
		t.Fatalf("profiles: %+v %v", list, err)
	}
	p, err = SaveProfile(root, p.ID, "renamed", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	list, err = Profiles(root)
	if err != nil || len(list) != 1 || list[0].Name != "renamed" {
		t.Fatalf("updated profiles: %+v %v", list, err)
	}
	if runtime.GOOS != "windows" {
		for _, entry := range []struct {
			path string
			mode os.FileMode
		}{{root, 0700}, {filepath.Dir(p.Path), 0700}, {p.Path, 0600}, {filepath.Join(filepath.Dir(p.Path), "name.json"), 0600}} {
			info, err := os.Stat(entry.path)
			if err != nil || info.Mode().Perm() != entry.mode {
				t.Fatalf("permissions %s: %v %v", entry.path, info, err)
			}
		}
	}
	if err = DeleteProfile(root, p.ID); err != nil {
		t.Fatal(err)
	}
	list, err = Profiles(root)
	if err != nil || len(list) != 0 {
		t.Fatalf("delete: %+v %v", list, err)
	}
}
func TestRemoteProfileMetadataRoundTrip(t *testing.T) {
	root := t.TempDir()
	profile, err := SaveProfileWithMetadata(root, Profile{Type: "remote", RemoteURL: "https://example.com/config.json", AutoUpdate: true, AutoUpdateInterval: 45, LastUpdated: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}, "Remote", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := Profiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0] != profile || profiles[0].Type != "remote" || profiles[0].RemoteURL != "https://example.com/config.json" || profiles[0].AutoUpdateInterval != 45 {
		t.Fatalf("remote metadata did not round-trip: %#v (saved %#v)", profiles, profile)
	}
}

func TestAPIConflictAndInvalidConfig(t *testing.T) {
	source := []byte(`{"services":[{"type":"api","secret":"old"},{"type":"resolved","tag":"keep"},{"type":"api"}],"unknown":true}`)
	if _, err := deriveConfig(source, 1, "new", false); !errors.Is(err, ErrAPIConflict) {
		t.Fatalf("want API conflict: %v", err)
	}
	result, err := deriveConfig(source, 1, "new", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "old") || strings.Count(string(result), `"api"`) != 1 || !strings.Contains(string(result), "keep") {
		t.Fatalf("replacement lost service: %s", result)
	}
	for _, bad := range []string{"null", "[]", "{", "{\"services\":{}}", "{\"services\":null}"} {
		if _, err = deriveConfig([]byte(bad), 1, "new", false); err == nil {
			t.Fatalf("accepted invalid config %s", bad)
		}
	}
	root := t.TempDir()
	for _, id := range []string{"../x", "x", "/tmp/x"} {
		if _, err = SaveProfile(root, id, "x", DefaultConfig()); err == nil {
			t.Fatalf("accepted bad ID %q", id)
		}
		if err = DeleteProfile(root, id); err == nil {
			t.Fatalf("deleted bad ID %q", id)
		}
	}
	if _, err = SaveProfile(root, "", " ", DefaultConfig()); err == nil {
		t.Fatal("accepted blank name")
	}
	if _, err = SaveProfile(root, "", "x", strings.Repeat(" ", int(maxConfigSize)+1)); err == nil {
		t.Fatal("accepted oversized profile")
	}
	config, err := parseConfig([]byte(DefaultConfig()))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := config["inbounds"]; exists {
		t.Fatal("default opens an inbound")
	}
	if _, exists := config["services"]; exists {
		t.Fatal("default config has API")
	}
	if strings.Contains(DefaultConfig(), "tun") || strings.Contains(DefaultConfig(), "system_proxy") {
		t.Fatal("unsafe default")
	}
}
func TestConfigHasTUNInbound(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want bool
	}{
		{name: "tun inbound", json: `{"inbounds":[{"type":"tun"}]}`, want: true},
		{name: "other inbound", json: `{"inbounds":[{"type":"mixed"}]}`},
		{name: "tun in unrelated string", json: `{"description":"tun","inbounds":[{"type":"mixed"}]}`},
		{name: "missing inbounds", json: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ConfigHasTUNInbound([]byte(tc.json))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("ConfigHasTUNInbound() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseConfigDepthBoundaries(t *testing.T) {
	// Fixtures stop at one past the limit; do not feed resource-exhausting
	// depths into the recursive JSONC parser, even in a regression test.
	for _, kind := range []string{"arrays", "objects", "mixed"} {
		for _, depth := range []int{maxConfigDepth - 1, maxConfigDepth, maxConfigDepth + 1} {
			var input strings.Builder
			input.WriteString(`{"value":`)
			closing := "}"
			for i := 1; i < depth; i++ {
				if kind == "objects" || (kind == "mixed" && i%2 == 0) {
					input.WriteString(`{"value":`)
					closing = "}" + closing
				} else {
					input.WriteByte('[')
					closing = "]" + closing
				}
			}
			input.WriteByte('0')
			input.WriteString(closing)
			_, err := parseConfig([]byte(input.String()))
			if depth <= maxConfigDepth && err != nil {
				t.Fatalf("%s depth %d: %v", kind, depth, err)
			}
			if depth > maxConfigDepth && (err == nil || !strings.Contains(err.Error(), "nesting depth")) {
				t.Fatalf("%s depth %d: want depth rejection, got %v", kind, depth, err)
			}
		}
	}
	// An incomplete document must hit our guard before hujson's EOF error.
	if _, err := parseConfig([]byte(strings.Repeat("[", maxConfigDepth+1))); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("preflight did not reject excessive nesting: %v", err)
	}
}

func TestParseConfigDepthJSONCLexing(t *testing.T) {
	brackets := strings.Repeat("[{", maxConfigDepth+1)
	closers := strings.Repeat("]}", maxConfigDepth+1)
	// 覆盖引号前奇数或偶数个反斜杠、字符串中的注释标记，
	// 以及注释中的引号、反斜杠和注释标记。
	value := `quote: " backslash: \ escaped quote: \" // /* */ ` + brackets + closers
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"value":` + string(encoded) + `}`,
		"// \" \\ /* " + brackets + "\r\n{\"value\":" + string(encoded) + ", // " + closers + "\n}\n",
		"/* \" \\ // " + brackets + " */ {\"value\":" + string(encoded) + ", /* " + closers + " */}\n",
		`{"value":"ends in backslash\\", "next":[]}`,
	} {
		if _, err := parseConfig([]byte(input)); err != nil {
			t.Fatalf("valid JSONC rejected: %v", err)
		}
	}
	config, err := parseConfig([]byte(`{"value":` + string(encoded) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded string
	if err := json.Unmarshal(config["value"], &decoded); err != nil || decoded != value {
		t.Fatalf("string changed: %q, %v", decoded, err)
	}
	// 注释或字符串中的结束括号不能减少真实嵌套深度。
	for _, prefix := range []string{
		"/* " + closers + " */",
		"// " + closers + "\n",
		`{"ignored":"` + closers + `", "value":`,
	} {
		input := prefix + strings.Repeat("[", maxConfigDepth+1) + "0" + strings.Repeat("]", maxConfigDepth+1)
		if strings.HasPrefix(prefix, "{") {
			input += "}"
		}
		if _, err := parseConfig([]byte(input)); err == nil || !strings.Contains(err.Error(), "nesting depth") {
			t.Fatalf("hidden closers bypassed depth guard: %v", err)
		}
	}
	for _, input := range []string{`{"x":"unterminated`, `{"x":"trailing\`, `{} /* unterminated`, "{} // no newline", `{"x": /}`, `{"x":[}`, `}{`, `{"x":"bad\q"}`} {
		if _, err := parseConfig([]byte(input)); err == nil {
			t.Fatalf("malformed JSONC accepted: %q", input)
		}
	}
}

func TestProfileRejectsSymlinksAndCorruption(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege dependent")
	}
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, "profiles")); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveProfile(root, "", "name", DefaultConfig()); err == nil {
		t.Fatal("followed profiles symlink")
	}
	root = t.TempDir()
	p, err := SaveProfile(root, "", "name", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(p.Path), "name.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Profiles(root); err == nil {
		t.Fatal("ignored corrupt metadata")
	}
	if err = os.Remove(p.Path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(external, "config.json"), p.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadProfile(p); err == nil {
		t.Fatal("followed config symlink")
	}
}
