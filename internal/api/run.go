package api

import (
	"context"
	"io"
	"sync"
	"time"

	"boxpilot/internal/gen/daemon"
	"boxpilot/internal/model"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

func terminal(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnimplemented, connect.CodeNotFound, connect.CodeUnauthenticated, connect.CodePermissionDenied:
		return true
	}
	return false
}
func backoff(ctx context.Context, attempt int) bool {
	delay := time.Duration(min(attempt, 5)) * time.Second
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type event struct {
	generation int
	apply      func(*cache)
}

// Run owns all mutable cache state in one goroutine. Publications are detached
// snapshots delivered serially, at most every 100ms (apart from initial probe
// transitions). publish should return promptly. Cancellation joins every worker.
func (c *Client) Run(parent context.Context, publish func(Snapshot)) {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait(); c.Close() }()
	state := newCache()
	output := func() {
		if publish != nil && ctx.Err() == nil {
			publish(state.snapshot())
		}
	}
	output()
	for attempt := 1; ctx.Err() == nil; attempt++ {
		version, err := unary(ctx, &emptypb.Empty{}, c.rpc.GetVersion)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			state.Version, state.APIVersion = version.Version, version.ApiVersion
			state.Phase, state.Error = "connecting", ""
			output()
			break
		}
		state.Phase, state.Error = "error", err.Error()
		output()
		if terminal(err) {
			return
		}
		if !backoff(ctx, attempt) {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	events := make(chan event, 128)
	send := func(streamCtx context.Context, generation int, fn func(*cache)) {
		if streamCtx.Err() != nil {
			return
		}
		select {
		case <-streamCtx.Done():
		case events <- event{generation, fn}:
		}
	}
	// Work emits successful updates. Transient failures reconnect with a bounded
	// backoff; permanent errors remain visible without retrying forever.
	launch := func(streamCtx context.Context, generation int, name string, once bool, work func(func(func(*cache))) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			attempt := 0
			for streamCtx.Err() == nil {
				err := work(func(fn func(*cache)) {
					attempt = 0
					send(streamCtx, generation, func(s *cache) { delete(s.StreamErrors, name); fn(s) })
				})
				if streamCtx.Err() != nil {
					return
				}
				if err == nil && once {
					return
				}
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				send(streamCtx, generation, func(s *cache) {
					s.StreamErrors[name] = err.Error()
					if name == "serviceStatus" {
						s.Phase, s.Error = "error", err.Error()
						s.ServiceStatus = nil
					}
				})
				if terminal(err) {
					return
				}
				attempt++
				if !backoff(streamCtx, attempt) {
					return
				}
			}
		}()
	}
	launch(ctx, 0, "serviceStatus", false, func(update func(func(*cache))) error {
		return consume(ctx, &emptypb.Empty{}, c.rpc.SubscribeServiceStatus, func(msg *daemon.ServiceStatus) {
			update(func(s *cache) {
				s.ServiceStatus = msg
				s.Phase, s.Error = "active", ""
				if msg.Status == daemon.ServiceStatus_FATAL {
					s.Phase, s.Error = "error", msg.ErrorMessage
				}
			})
		})
	})
	launch(ctx, 0, "logs", false, func(update func(func(*cache))) error {
		return consume(ctx, &emptypb.Empty{}, c.rpc.SubscribeLog, func(msg *daemon.Log) { update(func(s *cache) { s.logs(msg) }) })
	})
	generation := 0
	var runningCancel context.CancelFunc
	start := func() {
		streamCtx, stop := context.WithCancel(ctx)
		runningCancel = stop
		scope := generation
		launch(streamCtx, scope, "status", false, func(update func(func(*cache))) error {
			return consume(streamCtx, &daemon.SubscribeStatusRequest{Interval: int64(time.Second)}, c.rpc.SubscribeStatus, func(msg *daemon.Status) { update(func(s *cache) { s.status(msg) }) })
		})
		launch(streamCtx, scope, "groups", false, func(update func(func(*cache))) error {
			return consume(streamCtx, &emptypb.Empty{}, c.rpc.SubscribeGroups, func(msg *daemon.Groups) { update(func(s *cache) { s.Groups = msg.Group }) })
		})
		launch(streamCtx, scope, "connections", false, func(update func(func(*cache))) error {
			// Delta-only updates from a new stream must never merge into old rows.
			update(func(s *cache) { s.rows = map[string]model.ConnectionRow{} })
			return consume(streamCtx, &daemon.SubscribeConnectionsRequest{Interval: int64(time.Second)}, c.rpc.SubscribeConnections, func(msg *daemon.ConnectionEvents) { update(func(s *cache) { s.connections(msg) }) })
		})
		launch(streamCtx, scope, "outbounds", false, func(update func(func(*cache))) error {
			return consume(streamCtx, &emptypb.Empty{}, c.rpc.SubscribeOutbounds, func(msg *daemon.OutboundList) { update(func(s *cache) { s.Outbounds = msg.Outbounds }) })
		})
		launch(streamCtx, scope, "clashMode", false, func(update func(func(*cache))) error {
			initial, err := unary(streamCtx, &emptypb.Empty{}, c.rpc.GetClashModeStatus)
			if err != nil {
				return err
			}
			update(func(s *cache) { s.Modes, s.CurrentMode = initial.ModeList, initial.CurrentMode })
			return consume(streamCtx, &emptypb.Empty{}, c.rpc.SubscribeClashMode, func(msg *daemon.ClashMode) { update(func(s *cache) { s.CurrentMode = msg.Mode }) })
		})
		launch(streamCtx, scope, "startedAt", true, func(update func(func(*cache))) error {
			started, err := unary(streamCtx, &emptypb.Empty{}, c.rpc.GetStartedAt)
			if err == nil {
				update(func(s *cache) { s.StartedAt = started.StartedAt })
			}
			return err
		})
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			if runningCancel != nil {
				runningCancel()
			}
			return
		case e := <-events:
			if ctx.Err() != nil {
				return
			}
			if e.generation != 0 && e.generation != generation {
				continue
			}
			e.apply(state)
			dirty = true
			shouldRun := state.ServiceStatus != nil && state.ServiceStatus.Status == daemon.ServiceStatus_STARTED
			if !shouldRun && runningCancel != nil {
				runningCancel()
				runningCancel = nil
				generation++
				state.Status = nil
				state.StartedAt = 0
				state.Groups = nil
				state.Outbounds = nil
				state.Modes = nil
				state.CurrentMode = ""
				state.rows = map[string]model.ConnectionRow{}
				state.UploadHistory, state.DownloadHistory = nil, nil
				for _, name := range []string{"status", "groups", "connections", "outbounds", "clashMode", "startedAt"} {
					delete(state.StreamErrors, name)
				}
			} else if shouldRun && runningCancel == nil {
				generation++
				start()
			}
		case <-ticker.C:
			if dirty {
				output()
				dirty = false
			}
		}
	}
}
