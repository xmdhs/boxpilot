package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/xmdhs/boxpilot/internal/gen/daemon"
)

func awaitSnapshot(t *testing.T, snapshots <-chan Snapshot, match func(Snapshot) bool) Snapshot {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case s := <-snapshots:
			if match(s) {
				return s
			}
		case <-timer.C:
			t.Fatal("snapshot condition timed out")
			return Snapshot{}
		}
	}
}
func waitDone(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not finish")
	}
}

func TestRunGatingMergeRetryAndCancel(t *testing.T) {
	transitions := make(chan daemon.ServiceStatus_Type, 4)
	var statusCalls, groupCalls, activeStreams atomic.Int32
	service := &testService{
		service: func(ctx context.Context, out *connect.ServerStream[daemon.ServiceStatus]) error {
			activeStreams.Add(1)
			defer activeStreams.Add(-1)
			if err := out.Send(&daemon.ServiceStatus{Status: daemon.ServiceStatus_IDLE}); err != nil {
				return err
			}
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case status := <-transitions:
					if err := out.Send(&daemon.ServiceStatus{Status: status}); err != nil {
						return err
					}
				}
			}
		},
		logs: func(ctx context.Context, out *connect.ServerStream[daemon.Log]) error {
			activeStreams.Add(1)
			defer activeStreams.Add(-1)
			if err := out.Send(&daemon.Log{Reset_: true, Messages: []*daemon.Log_Message{{Level: daemon.LogLevel_INFO, Message: "hello"}}}); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
		status: func(ctx context.Context, r *daemon.SubscribeStatusRequest, out *connect.ServerStream[daemon.Status]) error {
			if r.Interval != int64(time.Second) {
				t.Error("wrong status interval")
			}
			if statusCalls.Add(1) == 1 {
				return connect.NewError(connect.CodeUnavailable, errors.New("transient failure"))
			}
			activeStreams.Add(1)
			defer activeStreams.Add(-1)
			for i := range 35 {
				if err := out.Send(&daemon.Status{Uplink: int64(i), Downlink: int64(i + 10)}); err != nil {
					return err
				}
			}
			<-ctx.Done()
			return ctx.Err()
		},
		groups: func(context.Context, *connect.ServerStream[daemon.Groups]) error {
			groupCalls.Add(1)
			return connect.NewError(connect.CodeUnimplemented, errors.New("permanent groups failure"))
		},
		connections: func(ctx context.Context, r *daemon.SubscribeConnectionsRequest, out *connect.ServerStream[daemon.ConnectionEvents]) error {
			if r.Interval != int64(time.Second) {
				t.Error("wrong connection interval")
			}
			activeStreams.Add(1)
			defer activeStreams.Add(-1)
			err := out.Send(&daemon.ConnectionEvents{Reset_: true, Events: []*daemon.ConnectionEvent{
				{Id: "a", Connection: &daemon.Connection{Id: "a", UplinkTotal: 10}},
				{Id: "a", Type: daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE, UplinkDelta: 7, DownlinkDelta: 5},
				{Id: "a", Type: daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED, ClosedAt: 1000},
				{Id: "b", Connection: &daemon.Connection{Id: "b"}},
			}})
			if err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
		outbounds: func(ctx context.Context, out *connect.ServerStream[daemon.OutboundList]) error {
			activeStreams.Add(1)
			defer activeStreams.Add(-1)
			if err := out.Send(&daemon.OutboundList{Outbounds: []*daemon.GroupItem{{Tag: "proxy"}}}); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
		clash: func(ctx context.Context, out *connect.ServerStream[daemon.ClashMode]) error {
			activeStreams.Add(1)
			defer activeStreams.Add(-1)
			if err := out.Send(&daemon.ClashMode{Mode: "global"}); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	client := newTestClient(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots := make(chan Snapshot, 100)
	done := make(chan struct{})
	go func() { defer close(done); client.Run(ctx, func(s Snapshot) { snapshots <- s }) }()
	idle := awaitSnapshot(t, snapshots, func(s Snapshot) bool {
		return s.ServiceStatus != nil && s.ServiceStatus.Status == daemon.ServiceStatus_IDLE && len(s.Logs) == 1
	})
	if idle.Version != "test-version" || idle.APIVersion != 42 || idle.Phase != "active" {
		t.Fatalf("bad initial snapshot: %+v", idle)
	}
	if statusCalls.Load() != 0 || groupCalls.Load() != 0 {
		t.Fatal("instance streams started while idle")
	}
	transitions <- daemon.ServiceStatus_STARTED
	first := awaitSnapshot(t, snapshots, func(s Snapshot) bool {
		return s.Status != nil && s.Status.Uplink == 34 && s.CurrentMode == "global" && len(s.Connections) == 2 && s.StartedAt == 123456 && len(s.Outbounds) == 1
	})
	if len(first.UploadHistory) != 30 || first.UploadHistory[0] != 5 || first.DownloadHistory[29] != 44 {
		t.Fatal("history not bounded")
	}
	if first.Connections[0].ClosedAt != 1000 || first.Connections[0].UplinkTotal != 17 || first.Connections[0].UplinkRate != 0 {
		t.Fatal(first.Connections)
	}
	if first.StreamErrors["groups"] == "" || first.StreamErrors["status"] != "" {
		t.Fatal(first.StreamErrors)
	}
	if statusCalls.Load() != 2 || groupCalls.Load() != 1 {
		t.Fatalf("retry calls status=%d groups=%d", statusCalls.Load(), groupCalls.Load())
	}
	// The earlier publication must remain unchanged after subsequent state transitions.
	transitions <- daemon.ServiceStatus_IDLE
	stopped := awaitSnapshot(t, snapshots, func(s Snapshot) bool {
		return s.ServiceStatus != nil && s.ServiceStatus.Status == daemon.ServiceStatus_IDLE && s.Status == nil
	})
	if len(stopped.Connections) != 0 || len(stopped.UploadHistory) != 0 || len(stopped.Logs) != 1 {
		t.Fatal("stop reset wrong caches")
	}
	if first.Status.Uplink != 34 || len(first.Connections) != 2 || len(first.UploadHistory) != 30 {
		t.Fatal("old snapshot changed")
	}
	transitions <- daemon.ServiceStatus_STARTED
	awaitSnapshot(t, snapshots, func(s Snapshot) bool { return s.Status != nil && s.CurrentMode == "global" && len(s.Connections) == 2 })
	cancel()
	waitDone(t, done)
	// Server cancellation is asynchronous relative to HTTP client Close, so wait
	// for all observed server handlers with a bounded timer rather than sleeping.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for activeStreams.Load() != 0 {
		select {
		case <-tick.C:
		case <-timer.C:
			t.Fatalf("%d handlers leaked", activeStreams.Load())
		}
	}
	if statusCalls.Load() != 3 || groupCalls.Load() != 2 {
		t.Fatalf("unexpected reconnect after cancel status=%d groups=%d", statusCalls.Load(), groupCalls.Load())
	}
}

func TestPermanentProbeAndStreamErrors(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeUnimplemented, connect.CodeNotFound, connect.CodeUnauthenticated, connect.CodePermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			var calls atomic.Int32
			client := newTestClient(t, &testService{version: func(context.Context) (*daemon.Version, error) {
				calls.Add(1)
				return nil, connect.NewError(code, errors.New("terminal"))
			}})
			var last Snapshot
			done := make(chan struct{})
			go func() { defer close(done); client.Run(context.Background(), func(s Snapshot) { last = s }) }()
			waitDone(t, done)
			if calls.Load() != 1 || last.Phase != "error" || last.Error == "" {
				t.Fatalf("calls=%d snapshot=%+v", calls.Load(), last)
			}
		})
	}
}

func TestPermanentServiceStreamErrors(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeUnimplemented, connect.CodeNotFound, connect.CodeUnauthenticated, connect.CodePermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			var calls atomic.Int32
			client := newTestClient(t, &testService{service: func(context.Context, *connect.ServerStream[daemon.ServiceStatus]) error {
				calls.Add(1)
				return connect.NewError(code, errors.New("terminal stream"))
			}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			snapshots, done := make(chan Snapshot, 100), make(chan struct{})
			go func() { defer close(done); client.Run(ctx, func(s Snapshot) { snapshots <- s }) }()
			awaitSnapshot(t, snapshots, func(s Snapshot) bool { return s.Phase == "error" && s.StreamErrors["serviceStatus"] != "" })
			// A retry would happen after one second. Let that deadline pass.
			timer := time.NewTimer(1100 * time.Millisecond)
			<-timer.C
			cancel()
			waitDone(t, done)
			if calls.Load() != 1 {
				t.Fatalf("permanent stream retried %d times", calls.Load())
			}
		})
	}
}

func TestCancelDuringBackoff(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, &testService{version: func(context.Context) (*daemon.Version, error) {
		calls.Add(1)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("offline"))
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots, done := make(chan Snapshot, 10), make(chan struct{})
	go func() { defer close(done); client.Run(ctx, func(s Snapshot) { snapshots <- s }) }()
	awaitSnapshot(t, snapshots, func(s Snapshot) bool { return s.Phase == "error" })
	cancel()
	waitDone(t, done)
	if calls.Load() != 1 {
		t.Fatal("reconnected after cancellation")
	}
}

func TestCancelDuringProbe(t *testing.T) {
	started := make(chan struct{})
	serverDone := make(chan struct{})
	client := newTestClient(t, &testService{version: func(ctx context.Context) (*daemon.Version, error) {
		close(started)
		defer close(serverDone)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); client.Run(ctx, nil) }()
	waitDone(t, started)
	cancel()
	waitDone(t, done)
	waitDone(t, serverDone)
}
