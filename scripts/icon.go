//go:build ignore

// 生成独立应用图标；不使用上游 dashboard 的品牌图标。
package main

import (
	"log"
	"os"

	"boxpilot/internal/desktop"
)

func main() {
	if err := os.MkdirAll("resources", 0755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("resources/icon.png", desktop.IconPNG(512), 0644); err != nil {
		log.Fatal(err)
	}
}
