package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchRemoteConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = w.Write([]byte(DefaultConfig()))
	}))
	defer server.Close()
	content, err := FetchRemoteConfig(context.Background(), server.URL+"/config.json")
	if err != nil || content != DefaultConfig() {
		t.Fatalf("fetch remote profile: content=%q err=%v", content, err)
	}
}

func TestFetchRemoteConfigRejectsInvalidAndOversizedBodies(t *testing.T) {
	for name, body := range map[string]string{
		"invalid":   "not a config",
		"oversized": strings.Repeat(" ", int(maxConfigSize)+1),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			if _, err := FetchRemoteConfig(context.Background(), server.URL); err == nil {
				t.Fatal("accepted invalid remote config")
			}
		})
	}
}

func TestValidateRemoteURL(t *testing.T) {
	for _, raw := range []string{"https://example.com/config.json", "http://127.0.0.1:8080/profile"} {
		if _, err := ValidateRemoteURL(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "file:///etc/passwd", "https://user:pass@example.com/profile", "https:///missing-host"} {
		if _, err := ValidateRemoteURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
