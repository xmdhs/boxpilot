package app

import (
	"testing"

	"github.com/xmdhs/boxpilot/locales"
)

func TestCatalogLocalesAndDynamicMessages(t *testing.T) {
	if got := locales.In("en").Overview(); got != "Overview" {
		t.Fatalf("English overview = %q", got)
	}
	if got := locales.In("zh-Hans").Overview(); got != "概述" {
		t.Fatalf("Simplified Chinese overview = %q", got)
	}
	if got := locales.In("en").DurationSeconds("30"); got != "30 seconds" {
		t.Fatalf("English duration = %q", got)
	}
	if got := locales.In("zh-Hans").DurationSeconds("30"); got != "30 秒" {
		t.Fatalf("Simplified Chinese duration = %q", got)
	}
	if got := locales.In("en").RemoteProfileImportConfirmation("Home", "example.com"); got != "Are you sure to import Home? You will connect to example.com to download the configuration." {
		t.Fatalf("English remote-profile confirmation = %q", got)
	}
	if got := locales.In("zh-Hans").RemoteProfileImportConfirmation("家庭", "example.com"); got != "您确定要导入 家庭 吗？将会连接到 example.com 下载配置。" {
		t.Fatalf("Simplified Chinese remote-profile confirmation = %q", got)
	}
	if got := locales.Catalog.Languages(); len(got) != 2 || got[0] != "en" || got[1] != "zh-Hans" {
		t.Fatalf("generated catalog languages = %v", got)
	}
}

func TestSetLanguageUpdatesStoredChoiceAndNavigation(t *testing.T) {
	a := &App{}
	a.setLanguage("en")
	if a.settings.Language != "en" || a.language() != "en" || pageLabel(a.language(), "overview") != "Overview" {
		t.Fatalf("English language state did not propagate: settings=%q label=%q", a.settings.Language, pageLabel(a.language(), "overview"))
	}
	a.setLanguage("zh-Hans")
	if a.settings.Language != "zh-Hans" || a.language() != "zh-Hans" || pageLabel(a.language(), "overview") != "概述" {
		t.Fatalf("Chinese language state did not propagate: settings=%q label=%q", a.settings.Language, pageLabel(a.language(), "overview"))
	}
}
