# 第三方授权与来源

## sing-box-dashboard

本项目的界面设计参考、StartedService 协议，以及部分状态归并/格式化/搜索和 ANSI 处理逻辑移植自工作区中的 sing-box-dashboard（https://github.com/SagerNet/sing-box-dashboard）。这些参考内容并非官方背书。

上游声明：Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>。上游按 GNU GPL version 3 或之后版本分发，并附有以下声明，现原样保留：

> In addition, no derivative work may use the name or imply association with this application without prior consent.

BoxPilot 使用独立名称与原创应用图标，不使用上游品牌作为应用名称，不暗示关联。完整 GPL 文本见根目录 `LICENSE`。使用和分发衍生内容时须保留适用授权、版权与源代码提供义务。

## MyGo

- 来源：https://github.com/egoist/mygo
- 使用本地 `../mygo` 检出，框架版本常量 0.8.3。
- MIT License，Copyright (c) 2026 MyGo contributors。
- 完整声明：`resources/licenses/mygo-MIT.txt`。

## Material Design Icons

- `internal/app/icons.go` 中的 SVG 路径来自 Material Design Icons，借由 dashboard 的生成资源移植。
- Apache License 2.0。
- 完整文本：`resources/licenses/MaterialIcons-Apache-2.0.txt`。

## Go 依赖

- connect-go（connectrpc.com/connect）：Apache-2.0。
- Protocol Buffers Go runtime 及生成代码所需库（google.golang.org/protobuf）：BSD-3-Clause。
- HuJSON（github.com/tailscale/hujson）：BSD-3-Clause。
- MyGo 的间接依赖见 `go.mod` / `go.sum`，其版权和许可保留于各 Go module。

本项目没有捆绑上游字体文件，文字使用系统字体与回退。sing-box 核心不嵌入源码或二进制，下载的核心属于其各自上游许可，分发核心时应一并遵守上游要求。
