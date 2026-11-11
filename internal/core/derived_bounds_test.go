package core

import (
	"strings"
	"testing"
)

func TestDerivedConfigDoesNotExpandNestedIndentation(t *testing.T) {
	content := `{"nested":` + strings.Repeat("[", 200) + "0" + strings.Repeat("]", 200) + "}"
	derived, err := deriveConfig([]byte(content), 9090, "test-secret", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(derived) > len(content)+1024 {
		t.Fatalf("runtime indentation expanded configuration: input=%d derived=%d", len(content), len(derived))
	}
}

func TestDerivedConfigHasAnOutputSizeLimit(t *testing.T) {
	// HTML 转义本身也会扩大输出，不能只检查原始输入大小。
	content := `{"padding":"` + strings.Repeat("<", int(maxConfigSize)/6) + `"}`
	if _, err := deriveConfig([]byte(content), 9090, "test-secret", false); err == nil || !strings.Contains(err.Error(), "derived configuration too large") {
		t.Fatalf("oversized derived output accepted: %v", err)
	}
}
