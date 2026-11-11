package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func ValidateRemoteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("remote profile URL must be an absolute http(s) URL without user info")
	}
	return u, nil
}

// FetchRemoteConfig downloads a bounded JSON/JSONC profile and validates its top-level shape.
func FetchRemoteConfig(ctx context.Context, raw string) (string, error) {
	u, err := ValidateRemoteURL(raw)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects while fetching remote profile")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("refusing HTTPS downgrade while fetching remote profile")
			}
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json, application/*+json, text/plain, */*;q=0.8")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download remote profile: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download remote profile: HTTP %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxConfigSize+1))
	if err != nil {
		return "", fmt.Errorf("read remote profile: %w", err)
	}
	if int64(len(body)) > maxConfigSize {
		return "", errors.New("remote profile exceeds the 8 MiB limit")
	}
	if _, err = parseConfig(body); err != nil {
		return "", fmt.Errorf("invalid remote profile: %w", err)
	}
	return string(body), nil
}

func UpdateRemoteProfile(root string, profile Profile, content string, updatedAt time.Time) (Profile, bool, error) {
	if profile.Type != "remote" {
		return Profile{}, false, errors.New("profile is not remote")
	}
	previous, err := ReadProfile(profile)
	if err != nil {
		return Profile{}, false, err
	}
	profile.LastUpdated = updatedAt
	updated, err := SaveProfileWithMetadata(root, profile, profile.Name, content)
	if err != nil {
		return Profile{}, false, err
	}
	return updated, previous != content, nil
}
