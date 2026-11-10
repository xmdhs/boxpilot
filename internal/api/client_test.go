package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"boxpilot/internal/gen/daemon"
	"boxpilot/internal/gen/daemon/daemonconnect"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testService struct {
	daemonconnect.UnimplementedStartedServiceHandler
	version     func(context.Context) (*daemon.Version, error)
	service     func(context.Context, *connect.ServerStream[daemon.ServiceStatus]) error
	logs        func(context.Context, *connect.ServerStream[daemon.Log]) error
	status      func(context.Context, *daemon.SubscribeStatusRequest, *connect.ServerStream[daemon.Status]) error
	groups      func(context.Context, *connect.ServerStream[daemon.Groups]) error
	connections func(context.Context, *daemon.SubscribeConnectionsRequest, *connect.ServerStream[daemon.ConnectionEvents]) error
	outbounds   func(context.Context, *connect.ServerStream[daemon.OutboundList]) error
	clash       func(context.Context, *connect.ServerStream[daemon.ClashMode]) error
	network     func(context.Context, *daemon.NetworkQualityTestRequest, *connect.ServerStream[daemon.NetworkQualityTestProgress]) error
	stun        func(context.Context, *daemon.STUNTestRequest, *connect.ServerStream[daemon.STUNTestProgress]) error
	mu          sync.Mutex
	calls       map[string]string
}

func (s *testService) record(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]string{}
	}
	s.calls[name] = value
}
func (s *testService) GetVersion(ctx context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[daemon.Version], error) {
	if s.version != nil {
		msg, err := s.version(ctx)
		return connect.NewResponse(msg), err
	}
	return connect.NewResponse(&daemon.Version{Version: "test-version", ApiVersion: 42}), nil
}
func (s *testService) URLTest(_ context.Context, r *connect.Request[daemon.URLTestRequest]) (*connect.Response[emptypb.Empty], error) {
	s.record("url", r.Msg.OutboundTag)
	return connect.NewResponse(&emptypb.Empty{}), nil
}
func (s *testService) SelectOutbound(_ context.Context, r *connect.Request[daemon.SelectOutboundRequest]) (*connect.Response[emptypb.Empty], error) {
	s.record("select", r.Msg.GroupTag+"/"+r.Msg.OutboundTag)
	return connect.NewResponse(&emptypb.Empty{}), nil
}
func (s *testService) SetGroupExpand(_ context.Context, r *connect.Request[daemon.SetGroupExpandRequest]) (*connect.Response[emptypb.Empty], error) {
	s.record("expand", fmt.Sprint(r.Msg.GroupTag, "/", r.Msg.IsExpand))
	return connect.NewResponse(&emptypb.Empty{}), nil
}
func (s *testService) SetClashMode(_ context.Context, r *connect.Request[daemon.ClashMode]) (*connect.Response[emptypb.Empty], error) {
	s.record("mode", r.Msg.Mode)
	return connect.NewResponse(&emptypb.Empty{}), nil
}
func (s *testService) CloseConnection(_ context.Context, r *connect.Request[daemon.CloseConnectionRequest]) (*connect.Response[emptypb.Empty], error) {
	s.record("close", r.Msg.Id)
	return connect.NewResponse(&emptypb.Empty{}), nil
}
func (s *testService) CloseAllConnections(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
	s.record("closeAll", "yes")
	return connect.NewResponse(&emptypb.Empty{}), nil
}
func (s *testService) ClearLogs(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
	s.record("clear", "yes")
	return connect.NewResponse(&emptypb.Empty{}), nil
}
func (s *testService) GetStartedAt(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[daemon.StartedAt], error) {
	return connect.NewResponse(&daemon.StartedAt{StartedAt: 123456}), nil
}
func (s *testService) GetClashModeStatus(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[daemon.ClashModeStatus], error) {
	return connect.NewResponse(&daemon.ClashModeStatus{ModeList: []string{"rule", "global"}, CurrentMode: "rule"}), nil
}
func (s *testService) SubscribeServiceStatus(ctx context.Context, _ *connect.Request[emptypb.Empty], out *connect.ServerStream[daemon.ServiceStatus]) error {
	if s.service == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("service unsupported"))
	}
	return s.service(ctx, out)
}
func (s *testService) SubscribeLog(ctx context.Context, _ *connect.Request[emptypb.Empty], out *connect.ServerStream[daemon.Log]) error {
	if s.logs == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("logs unsupported"))
	}
	return s.logs(ctx, out)
}
func (s *testService) SubscribeStatus(ctx context.Context, r *connect.Request[daemon.SubscribeStatusRequest], out *connect.ServerStream[daemon.Status]) error {
	if s.status == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("status unsupported"))
	}
	return s.status(ctx, r.Msg, out)
}
func (s *testService) SubscribeGroups(ctx context.Context, _ *connect.Request[emptypb.Empty], out *connect.ServerStream[daemon.Groups]) error {
	if s.groups == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("groups unsupported"))
	}
	return s.groups(ctx, out)
}
func (s *testService) SubscribeConnections(ctx context.Context, r *connect.Request[daemon.SubscribeConnectionsRequest], out *connect.ServerStream[daemon.ConnectionEvents]) error {
	if s.connections == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("connections unsupported"))
	}
	return s.connections(ctx, r.Msg, out)
}
func (s *testService) SubscribeOutbounds(ctx context.Context, _ *connect.Request[emptypb.Empty], out *connect.ServerStream[daemon.OutboundList]) error {
	if s.outbounds == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("outbounds unsupported"))
	}
	return s.outbounds(ctx, out)
}
func (s *testService) SubscribeClashMode(ctx context.Context, _ *connect.Request[emptypb.Empty], out *connect.ServerStream[daemon.ClashMode]) error {
	if s.clash == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("mode unsupported"))
	}
	return s.clash(ctx, out)
}
func (s *testService) StartNetworkQualityTest(ctx context.Context, r *connect.Request[daemon.NetworkQualityTestRequest], out *connect.ServerStream[daemon.NetworkQualityTestProgress]) error {
	return s.network(ctx, r.Msg, out)
}
func (s *testService) StartSTUNTest(ctx context.Context, r *connect.Request[daemon.STUNTestRequest], out *connect.ServerStream[daemon.STUNTestProgress]) error {
	return s.stun(ctx, r.Msg, out)
}

func newTestClient(t *testing.T, service *testService) *Client {
	t.Helper()
	_, handler := daemonconnect.NewStartedServiceHandler(service)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/prefix/daemon.StartedService/") {
			t.Errorf("lost URL prefix: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer top-secret" || r.Header.Get("Accept-Language") != "zh-CN" {
			t.Errorf("missing headers: %v", r.Header)
		}
		if r.Header.Get("Content-Type") != "application/grpc-web+proto" || r.Header.Get("X-User-Agent") == "" {
			t.Errorf("not grpc-web: %v", r.Header)
		}
		if strings.Contains(r.URL.Path, "Subscribe") || strings.Contains(r.URL.Path, "StartSTUN") || strings.Contains(r.URL.Path, "StartNetwork") {
			if r.Header.Get("Grpc-Timeout") != "" {
				t.Errorf("stream has global deadline: %v", r.Header)
			}
		}
		http.StripPrefix("/prefix", handler).ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := New(Server{URL: server.URL + "/prefix/", Secret: "top-secret"}, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestUnaryGRPCWeb(t *testing.T) {
	service := &testService{}
	client := newTestClient(t, service)
	if client.transport.Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	ctx := context.Background()
	for _, call := range []func() error{
		func() error { return client.Probe(ctx) }, func() error { return client.URLTest(ctx, "out") },
		func() error { return client.SelectOutbound(ctx, "group", "tag") }, func() error { return client.SetGroupExpand(ctx, "group", true) },
		func() error { return client.SetClashMode(ctx, "global") }, func() error { return client.CloseConnection(ctx, "id") },
		func() error { return client.CloseAllConnections(ctx) }, func() error { return client.ClearLogs(ctx) },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	for key, want := range map[string]string{"url": "out", "select": "group/tag", "expand": "group/true", "mode": "global", "close": "id", "closeAll": "yes", "clear": "yes"} {
		if service.calls[key] != want {
			t.Errorf("%s: %q", key, service.calls[key])
		}
	}
}

func TestDiagnosticsStreamsAndCancel(t *testing.T) {
	canceled := make(chan struct{})
	service := &testService{
		network: func(ctx context.Context, r *daemon.NetworkQualityTestRequest, out *connect.ServerStream[daemon.NetworkQualityTestProgress]) error {
			if r.OutboundTag != "proxy" || r.ConfigURL != "https://config.invalid/test" || r.MaxRuntimeSeconds != 60 || !r.Serial || !r.Http3 {
				t.Errorf("bad network request: %v", r)
			}
			if err := out.Send(&daemon.NetworkQualityTestProgress{Phase: 1, DownloadCapacity: 100}); err != nil {
				return err
			}
			return out.Send(&daemon.NetworkQualityTestProgress{Phase: 2, IsFinal: true})
		},
		stun: func(ctx context.Context, r *daemon.STUNTestRequest, out *connect.ServerStream[daemon.STUNTestProgress]) error {
			defer close(canceled)
			if r.Server != "stun.invalid:3478" || r.OutboundTag != "proxy" {
				t.Errorf("bad STUN request: %v", r)
			}
			if err := out.Send(&daemon.STUNTestProgress{Phase: 1, ExternalAddr: "1.2.3.4"}); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	client := newTestClient(t, service)
	var progress []*daemon.NetworkQualityTestProgress
	err := client.NetworkQuality(context.Background(), &daemon.NetworkQualityTestRequest{ConfigURL: "https://config.invalid/test", OutboundTag: "proxy", MaxRuntimeSeconds: 60, Serial: true, Http3: true}, func(p *daemon.NetworkQualityTestProgress) { progress = append(progress, p) })
	if err != nil || len(progress) != 2 || !progress[1].IsFinal {
		t.Fatalf("network: %v %#v", err, progress)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = client.STUN(ctx, &daemon.STUNTestRequest{Server: "stun.invalid:3478", OutboundTag: "proxy"}, func(p *daemon.STUNTestProgress) {
		if p.ExternalAddr != "1.2.3.4" {
			t.Error(p)
		}
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("server stream not canceled")
	}
}

func TestURLValidationRedirectAndTLS(t *testing.T) {
	for _, raw := range []string{"", "ftp://localhost", "http://", "http://user:pass@localhost", "http://localhost?query=x", "http://localhost/#frag"} {
		if _, err := New(Server{URL: raw}, "en"); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := New(Server{URL: strings.TrimPrefix(redirect.URL, "http://"), Secret: "secret"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Probe(context.Background()); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	if leaked.Load() != 0 {
		t.Fatal("secret request reached redirect target")
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tlsServer.Close()
	tlsClient, err := New(Server{URL: tlsServer.URL}, "en")
	if err != nil {
		t.Fatal(err)
	}
	defer tlsClient.Close()
	if err := tlsClient.Probe(context.Background()); err == nil {
		t.Fatal("untrusted TLS accepted")
	}
}
