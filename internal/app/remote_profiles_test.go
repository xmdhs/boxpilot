package app

import (
	"testing"

	"boxpilot/internal/core"
)

func TestParseRemoteProfileImportLink(t *testing.T) {
	name, remote, err := parseRemoteProfileImportLink("sing-box://import-remote-profile?url=https%3A%2F%2Fexample.com%2Fprofile.json%3Ftoken%3Da%252Fb#My%20Profile")
	if err != nil {
		t.Fatal(err)
	}
	if name != "My Profile" || remote != "https://example.com/profile.json?token=a%2Fb" {
		t.Fatalf("parsed name=%q remote=%q", name, remote)
	}
}

func TestParseRemoteProfileImportLinkRejectsMalformedAndUnsupported(t *testing.T) {
	if _, _, err := parseRemoteProfileImportLink("https://example.com"); err != errUnsupportedOpenURL {
		t.Fatalf("unsupported scheme error = %v", err)
	}
	for _, raw := range []string{
		"sing-box://import-remote-profile",
		"sing-box://import-remote-profile?url=file%3A%2F%2F%2Fetc%2Fpasswd",
	} {
		_, remote, err := parseRemoteProfileImportLink(raw)
		if err == nil {
			if _, validateErr := validateRemoteLinkTarget(remote); validateErr == nil {
				t.Errorf("accepted unsafe import target %q", remote)
			}
		}
	}
}

func validateRemoteLinkTarget(raw string) (string, error) {
	parsed, err := core.ValidateRemoteURL(raw)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}
