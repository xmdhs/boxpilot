package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The only executable installed or launched in unit tests is this local test binary.
func TestMain(m *testing.M) {
	if os.Getenv("BOXPILOT_CORE_HELPER") == "1" && len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "check", "run":
			helperProcess()
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}
func grpcWebReply(w http.ResponseWriter, code string, data bool) {
	w.Header().Set("Content-Type", "application/grpc-web+proto")
	if data {
		w.Write([]byte{0, 0, 0, 0, 0})
	}
	trailer := []byte("grpc-status: " + code + "\r\n")
	header := make([]byte, 5)
	header[0] = 0x80
	binary.BigEndian.PutUint32(header[1:], uint32(len(trailer)))
	w.Write(header)
	w.Write(trailer)
}
func helperProcess() {
	if os.Args[1] == "version" {
		version := os.Getenv("BOXPILOT_HELPER_VERSION")
		if version == "" {
			version = "1.14.4"
		}
		fmt.Println("sing-box version " + version)
		fmt.Println("Environment: test helper only")
		return
	}
	if len(os.Args) < 4 || os.Args[2] != "-c" {
		os.Exit(12)
	}
	content, err := os.ReadFile(os.Args[3])
	if err != nil {
		os.Exit(13)
	}
	var cfg struct {
		Mode     string `json:"_test_mode"`
		Services []struct {
			Type      string `json:"type"`
			Listen    string `json:"listen"`
			Port      int    `json:"listen_port"`
			Secret    string `json:"secret"`
			Dashboard bool   `json:"dashboard"`
		} `json:"services"`
	}
	if json.Unmarshal(content, &cfg) != nil {
		os.Exit(14)
	}
	if os.Args[1] == "check" {
		if cfg.Mode == "check-fail" {
			fmt.Fprintln(os.Stderr, "check diagnostic")
			os.Exit(15)
		}
		if cfg.Mode == "check-wait" {
			if os.WriteFile(os.Args[3]+".checking", []byte("ready"), 0600) != nil {
				os.Exit(20)
			}
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, syscall.Signal(15), os.Interrupt)
			<-ch
		}
		return
	}
	if cfg.Mode == "exit" {
		fmt.Fprintln(os.Stderr, "early exit diagnostic")
		os.Exit(16)
	}
	if len(cfg.Services) == 0 {
		os.Exit(17)
	}
	api := cfg.Services[len(cfg.Services)-1]
	if api.Type != "api" || api.Listen != "127.0.0.1" || api.Secret == "" || api.Dashboard {
		os.Exit(18)
	}
	fmt.Fprintln(os.Stderr, "helper started, secret="+api.Secret)
	mux := http.NewServeMux()
	mux.HandleFunc("/daemon.StartedService/GetVersion", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/grpc-web+proto" || !bytes.Equal(body, []byte{0, 0, 0, 0, 0}) {
			http.Error(w, "wrong wire contract", 400)
			return
		}
		if cfg.Mode == "not-ready" {
			http.Error(w, "not ready", 503)
			return
		}
		if cfg.Mode != "no-auth" && r.Header.Get("Authorization") != "Bearer "+api.Secret {
			grpcWebReply(w, "16", false)
			return
		}
		grpcWebReply(w, "0", true)
	})
	mux.HandleFunc("/crash", func(http.ResponseWriter, *http.Request) { os.Exit(23) })
	ln, err := net.Listen("tcp4", net.JoinHostPort(api.Listen, strconv.Itoa(api.Port)))
	if err != nil {
		os.Exit(19)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go server.Serve(ln)
	sig := make(chan os.Signal, 1)
	if cfg.Mode == "ignore-stop" {
		signal.Ignore(syscall.Signal(15))
		signal.Ignore(os.Interrupt)
	} else {
		signal.Notify(sig, syscall.Signal(15), os.Interrupt)
	}
	<-sig
	fmt.Fprintln(os.Stderr, "gracefully stopped")
	server.Shutdown(context.Background())
}
func helperBinary(t *testing.T) string {
	t.Helper()
	t.Setenv("BOXPILOT_CORE_HELPER", "1")
	// 仅新启动的测试 helper 在运行时初始化时读取此选项；保留竞态检测及父进程
	// 的退出等待，避免子进程默认 1 秒退出等待耗尽 Manager 的 700ms 启动预算。
	t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return executable
}
func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("condition deadline exceeded")
		}
	}
}
