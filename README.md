# BoxPilot

使用 [MyGo](https://github.com/egoist/mygo) 的 `ui` 包编写的独立 **Go 原生桌面控制面板**。界面参考本工作区的 sing-box-dashboard，使用相同的 `daemon.StartedService` gRPC-Web API。

**无 WebView、无 HTML/JavaScript、无 Node/Bun 依赖。** 不隶属于 sing-box 或其 dashboard 项目。MyGo 在系统窗口中绘制原生 UI，Linux 使用 GTK 3 + OpenGL/软件绘制，macOS 使用 Metal，Windows 使用 D3D11/WARP。

## 功能

- 概览：实时速率、累计流量、历史曲线、内存、Goroutines、连接数、模式切换、卡片显示和排序。
- 分组：展开/折叠、节点选择、延迟状态、分组/单节点测速。
- 连接：虚拟列表，搜索、状态筛选、排序、详情，关闭单个或全部连接。
- 日志：ANSI 着色、Unicode 搜索高亮、级别筛选、暂停、自动滚底和复制；日志仅保存在内存中，不创建日志文件。
- 网络质量及 STUN 测试：由所选后端执行，支持参数、出站选择、进度和取消。
- 本机核心：查询官方发行版本、按系统架构下载并校验 SHA-256、安全安装；也支持选择已有二进制。
- 配置：新建、导入、编辑、复制、删除、校验、选择。导入不覆盖原件，启动生成独立运行副本。
- 本机受管进程：启动/停止、就绪检查、错误诊断、退出清理；不会按进程名关闭外部实例。
- 多实例连接：本机或远程 URL、密钥、连接测试、切换。
- 原生托盘、单实例唤醒、窗口位置记忆、可选开机启动、简体中文/英文、明暗主题和强调色。

首版不含 SSH、Tailscale、Taildrop、USB/IP、OpenVPN/OpenConnect 专用界面；不自动设置系统代理、不安装 Windows 服务。Windows 可选管理员模式：BoxPilot 界面保持普通权限，每次启动 sing-box 时由 UAC 单独确认。

## 环境与运行

目录布局：

```text
sing-box/
├── mygo/             # 本地框架检出
├── sing-box-dashboard/  # 参考实现，无需构建
└── boxpilot/         # 本应用
```

要求 **Go 1.27.3**；`go.mod` 的 `replace` 使用 `../mygo`。所有构建支持 `CGO_ENABLED=0`。

```sh
cd boxpilot
CGO_ENABLED=0 go tool mygo dev
```

也可直接 `CGO_ENABLED=0 go run .`。无 GUI 会话的机器不能用这条命令打开窗口。

### Linux 运行依赖

需要 GTK 3 及其字体/绘制依赖；托盘还需 Ayatana AppIndicator、D-Bus 会话以及支持 StatusNotifier 的桌面面板。**不需要 WebKitGTK**。

Debian/Ubuntu 对应的主要包通常为：

```sh
sudo apt install libgtk-3-0t64 libayatana-appindicator3-1 fonts-noto-cjk
```

旧发行版的 GTK 包名可能是 `libgtk-3-0`。字体使用系统回退，未安装 CJK 字体时应补装适当字体。GNOME 通常需要 AppIndicator 扩展；KDE 一般自带托盘宿主。程序通过 `gdbus` 或 `dbus-send` 检查托盘宿主。

若托盘库/宿主不可用，程序会提示并保留普通窗口行为，**不会把窗口隐藏到无法恢复的位置**。Windows/macOS 点击托盘恢复窗口，右键显示菜单；Linux 通过菜单的“显示 BoxPilot”恢复。

## 使用流程

### 管理本机核心

1. 打开“本机核心”，检查可用版本，选择并下载。仅从官方发行源下载，使用 GitHub 资产元数据中的 SHA-256 digest 验证；缺少 digest 或校验不一致时拒绝安装。
2. 新建默认配置或导入自己的配置，保存、校验并选择配置。
3. 点击“启动核心”。程序生成私有运行配置，添加随机密钥、随机端口的 `127.0.0.1` API，禁用 dashboard 静态站点，然后检查配置并启动。
4. 若已有 API 服务，程序先要求确认，**只修改运行副本**，不修改原始配置。
5. 关闭/最小化窗口可隐藏到托盘，核心继续运行；“退出应用”会停止本程序创建的核心并回收进程。

配置可能请求 TUN、修改路由/DNS 或开放监听端口，请自行核对。程序不自动为配置提权；权限、驱动或资源缺失会作为启动错误显示。默认配置无 TUN、不修改系统代理。

安装更新不会自动替换运行中的进程；选择版本后自行停止、重新启动。哈希校验保证与官方元数据一致，不等于独立签名认证。

### 连接已有实例

使用支持 `services.type=api` 的 sing-box，最低版本为 1.14.0；开发互通基线为 v1.14.4。不是旧版 Clash REST API。

已有实例的配置示例：

```json
{
  "services": [{
    "type": "api",
    "listen": "127.0.0.1",
    "listen_port": 9090,
    "secret": "请替换为随机密钥"
  }]
}
```

填写 `http://127.0.0.1:9090` 和对应密钥。远程建议使用 HTTPS；非回环 HTTP 需要明确确认明文风险。支持 URL 路径前缀。本应用不会启动/停止由其他程序创建的实例。

RPC 强制直连，不继承系统代理，TLS 正常验证，不支持全局跳过证书校验。下载器和 RPC 的网络用途相互独立。

### 凭据与数据

配置及元数据位于 MyGo 的 `PathUserData`（Linux 通常是 `~/.config/BoxPilot`，开发模式可能有 Dev 后缀）。POSIX 下私有目录为 0700，配置文件为 0600。Windows 的访问保护依赖用户目录 ACL，POSIX 权限值不等同于 Windows ACL。

密钥保存方式：

- `session`：仅本次会话，重启后需要重新输入。
- `keychain`：系统凭据库；不可用时明确报错，不会静默改存明文。
- `file`：只有明确同意后，才保存到独立的受限权限明文文件。

运行核心所需的配置本身可能包含密码、密钥等，请保护用户数据目录。损坏的设置文件不会被静默覆盖。日志和日志导出也可能含域名、地址等敏感信息。

## 测试与构建

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go tool mygo vet .
CGO_ENABLED=0 go tool mygo build -platform linux/arm64
```

`go tool mygo build` 负责图标和原生打包，`mygo.json` 已设置 `linux.webview: false`。未签名的跨平台可执行文件也可直接编译：

```sh
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags mygo_noinspector -o build/boxpilot.exe .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags mygo_noinspector -o build/boxpilot-macos .
```

macOS 签名、公证、DMG 和 Windows 安装器需要相应平台工具及身份，不由这两个命令提供。

若在本会话一样的受限环境中构建，将 Go 缓存放到可写的临时目录：

```sh
export GOPATH="$TMPDIR/boxpilot-go"
export GOCACHE="$TMPDIR/boxpilot-go-cache"
export GOMODCACHE="$TMPDIR/boxpilot-go-mod"
```

### 原生界面测试

`ui.NewTester` 无需显示器，测试页面、键盘、表单、虚拟列表和交互。截图输出：

```sh
BOXPILOT_SCREENSHOTS="$PWD/artifacts" CGO_ENABLED=0 go test -count=1 -run TestNativeSnapshots ./internal/app
```

Linux 的文字绘制仍需要 Pango/Cairo 和字体；如果输出 `text will not show`，交互测试通过**不代表文字渲染已验收**。真实托盘、输入法、系统缩放和窗口恢复必须在目标桌面验证。

### 显式真实核心互通测试

日常测试不下载、不执行远端核心。可显式指定已有二进制，或允许测试下载并校验固定的 v1.14.4：

```sh
BOXPILOT_REAL_CORE=/absolute/path/to/sing-box CGO_ENABLED=0 go test -count=1 -v ./internal/integration
# 会联网下载、验证并执行官方测试核心；仅使用临时目录和回环 API
BOXPILOT_REAL_CORE_DOWNLOAD=1 CGO_ENABLED=0 go test -count=1 -v ./internal/integration
```

测试的网络质量/STUN 探针使用本地无效目标，验证接口存在，不执行公网带宽测试。

### 重新生成协议和图标

```sh
sh scripts/generate.sh
CGO_ENABLED=0 go run scripts/icon.go
```

协议来自当前参考仓库的 `started_service.proto`。生成器版本固定，Go 生成文件已经包含在工程中，普通构建无需安装 protoc、buf 或 Node。

## 授权

见 `LICENSE` 和 `THIRD_PARTY_NOTICES.md`。本应用采用 GPL-3.0-or-later 兼容授权，保留移植内容的来源与上游名称/关联限制；MyGo 为 MIT；Material Design Icons 为 Apache-2.0。BoxPilot 使用独立名称和应用图标，不暗示与上游有官方关联。
