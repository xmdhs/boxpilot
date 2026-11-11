package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/xmdhs/boxpilot/internal/api"
	"github.com/xmdhs/boxpilot/internal/core"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
)

// 显式互通测试：日常 go test 不联网、不下载、不运行第三方程序。
// 可指定本地 binary，或显式允许下载经 SHA-256 校验的固定测试基线。
func TestRealCore(t *testing.T) {
	binary := os.Getenv("BOXPILOT_REAL_CORE")
	download := os.Getenv("BOXPILOT_REAL_CORE_DOWNLOAD") == "1"
	if binary == "" && !download {
		t.Skip("设置 BOXPILOT_REAL_CORE 或 BOXPILOT_REAL_CORE_DOWNLOAD=1 才运行真实核心互通")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	if binary == "" {
		releases, err := core.Releases(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		var release core.Release
		for _, r := range releases {
			if r.Version == "1.14.4" || r.Version == "v1.14.4" {
				release = r
				break
			}
		}
		if release.DownloadURL == "" {
			t.Fatal("官方列表未找到固定测试版本 v1.14.4")
		}
		installed, err := core.Install(ctx, root, release, nil)
		if err != nil {
			t.Fatal(err)
		}
		binary = installed.Path
		t.Log("已校验并安装测试核心", installed.Version)
	}
	profile, err := core.SaveProfile(root, "", "integration", core.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager := core.NewManager(root, nil)
	defer func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := manager.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	}()
	if err := manager.Start(ctx, binary, profile, false); err != nil {
		t.Fatal(err)
	}
	state := manager.State()
	if state.Phase != "running" || state.Secret == "" {
		t.Fatalf("核心未就绪: %s", state.Phase)
	}
	client, err := api.New(api.Server{URL: state.URL, Secret: state.Secret}, "zh-Hans")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	streamCtx, stopStreams := context.WithCancel(ctx)
	snapshots := make(chan api.Snapshot, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client.Run(streamCtx, func(s api.Snapshot) {
			select {
			case snapshots <- s:
			default:
			}
		})
	}()
	defer func() { stopStreams(); <-done }()
	for {
		select {
		case s := <-snapshots:
			if s.Version != "" && s.Status != nil && s.ServiceStatus.GetStatus() == daemon.ServiceStatus_STARTED {
				t.Logf("真实 gRPC-Web 互通通过: version=%s apiVersion=%d logs=%d", s.Version, s.APIVersion, len(s.Logs))
				goto ready
			}
		case <-ctx.Done():
			t.Fatal("状态订阅未就绪:", ctx.Err())
		}
	}
ready:
	if err := client.CloseAllConnections(ctx); err != nil {
		t.Fatal(err)
	}
	// 使用不出网的无效本地目标验证工具 RPC，允许预期的目标连接错误，不允许未实现。
	toolCtx, stopTool := context.WithTimeout(ctx, 4*time.Second)
	err = client.NetworkQuality(toolCtx, &daemon.NetworkQualityTestRequest{ConfigURL: "http://127.0.0.1:1/config", MaxRuntimeSeconds: 1}, func(*daemon.NetworkQualityTestProgress) {})
	stopTool()
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		t.Fatal("NetworkQuality RPC 未实现:", err)
	}
	toolCtx, stopTool = context.WithTimeout(ctx, 4*time.Second)
	err = client.STUN(toolCtx, &daemon.STUNTestRequest{Server: "127.0.0.1:1"}, func(*daemon.STUNTestProgress) {})
	stopTool()
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		t.Fatal("STUN RPC 未实现:", err)
	}
}
