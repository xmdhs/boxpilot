package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
	"github.com/xmdhs/boxpilot/internal/gen/daemon/daemonconnect"
	"github.com/xmdhs/boxpilot/internal/model"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Server struct{ ID, Name, URL, Secret string }

type Snapshot struct {
	Phase                          string
	Error                          string
	Version                        string
	APIVersion                     int32
	StartedAt                      int64
	ServiceStatus                  *daemon.ServiceStatus
	Status                         *daemon.Status
	UploadHistory, DownloadHistory []float64
	Groups                         []*daemon.Group
	Modes                          []string
	CurrentMode                    string
	Connections                    []model.ConnectionRow
	Logs                           []model.LogEntry
	Outbounds                      []*daemon.GroupItem
	StreamErrors                   map[string]string
}

type Client struct {
	rpc       daemonconnect.StartedServiceClient
	transport *http.Transport
}

type headerTransport struct {
	base             http.RoundTripper
	origin           *url.URL
	secret, language string
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}
func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !sameOrigin(req.URL, t.origin) {
		return nil, errors.New("refusing cross-origin RPC request")
	}
	req = req.Clone(req.Context())
	req.Header.Set("Accept-Language", t.language)
	if t.secret != "" {
		req.Header.Set("Authorization", "Bearer "+t.secret)
	}
	return t.base.RoundTrip(req)
}

func New(server Server, language string) (*Client, error) {
	raw := strings.TrimSpace(server.URL)
	if raw == "" {
		return nil, errors.New("server URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("server URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("server URL must be an HTTP(S) origin with an optional path prefix, without credentials, query or fragment")
	}
	if strings.ContainsAny(server.Secret+language, "\r\n") {
		return nil, errors.New("invalid RPC header value")
	}
	// Own the transport rather than inheriting mutable global proxy/TLS settings.
	// TLSClientConfig is nil, so Go performs normal certificate verification.
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		// No response-header or overall read timeout: streams may remain quiet.
	}
	client := &http.Client{Transport: headerTransport{transport, u, server.Secret, language}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if !sameOrigin(req.URL, u) {
			return errors.New("refusing cross-origin redirect")
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}}
	return &Client{rpc: daemonconnect.NewStartedServiceClient(client, strings.TrimRight(u.String(), "/"), connect.WithGRPCWeb()), transport: transport}, nil
}

func (c *Client) Close() {
	if c != nil && c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// Unary calls have a bounded lifetime; server streams use only their caller's context.
func unary[I, O any](ctx context.Context, msg *I, call func(context.Context, *connect.Request[I]) (*connect.Response[O], error)) (*O, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := call(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}
func (c *Client) Probe(ctx context.Context) error {
	_, err := unary(ctx, &emptypb.Empty{}, c.rpc.GetVersion)
	return err
}
func (c *Client) URLTest(ctx context.Context, tag string) error {
	_, err := unary(ctx, &daemon.URLTestRequest{OutboundTag: tag}, c.rpc.URLTest)
	return err
}
func (c *Client) SelectOutbound(ctx context.Context, group, tag string) error {
	_, err := unary(ctx, &daemon.SelectOutboundRequest{GroupTag: group, OutboundTag: tag}, c.rpc.SelectOutbound)
	return err
}
func (c *Client) SetGroupExpand(ctx context.Context, group string, expand bool) error {
	_, err := unary(ctx, &daemon.SetGroupExpandRequest{GroupTag: group, IsExpand: expand}, c.rpc.SetGroupExpand)
	return err
}
func (c *Client) SetClashMode(ctx context.Context, mode string) error {
	_, err := unary(ctx, &daemon.ClashMode{Mode: mode}, c.rpc.SetClashMode)
	return err
}
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	_, err := unary(ctx, &daemon.CloseConnectionRequest{Id: id}, c.rpc.CloseConnection)
	return err
}
func (c *Client) CloseAllConnections(ctx context.Context) error {
	_, err := unary(ctx, &emptypb.Empty{}, c.rpc.CloseAllConnections)
	return err
}
func (c *Client) ClearLogs(ctx context.Context) error {
	_, err := unary(ctx, &emptypb.Empty{}, c.rpc.ClearLogs)
	return err
}

func consume[I, O any](ctx context.Context, msg *I, call func(context.Context, *connect.Request[I]) (*connect.ServerStreamForClient[O], error), publish func(*O)) error {
	stream, err := call(ctx, connect.NewRequest(msg))
	if err != nil {
		return err
	}
	defer stream.Close()
	for stream.Receive() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if publish != nil {
			publish(stream.Msg())
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return stream.Err()
}
func (c *Client) NetworkQuality(ctx context.Context, request *daemon.NetworkQualityTestRequest, publish func(*daemon.NetworkQualityTestProgress)) error {
	if request == nil {
		return errors.New("network quality request is nil")
	}
	return consume(ctx, request, c.rpc.StartNetworkQualityTest, publish)
}
func (c *Client) STUN(ctx context.Context, request *daemon.STUNTestRequest, publish func(*daemon.STUNTestProgress)) error {
	if request == nil {
		return errors.New("STUN request is nil")
	}
	return consume(ctx, request, c.rpc.StartSTUNTest, publish)
}
