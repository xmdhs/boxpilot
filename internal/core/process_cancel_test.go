package core

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Separate Kill and exit gates prove that cleanup waits for reaping, not merely
// for cancellation or for submission of a kill request.
type cancellationTestProcess struct {
	killed    chan struct{}
	exit      chan struct{}
	killOnce  sync.Once
	waitCalls atomic.Int32
	waitErr   error
}

func newCancellationTestProcess() *cancellationTestProcess {
	return &cancellationTestProcess{killed: make(chan struct{}), exit: make(chan struct{})}
}
func (*cancellationTestProcess) PID() int            { return 1234 }
func (*cancellationTestProcess) GracefulStop() error { return nil }
func (p *cancellationTestProcess) Kill() error {
	p.killOnce.Do(func() { close(p.killed) })
	return nil
}
func (p *cancellationTestProcess) Wait() error {
	p.waitCalls.Add(1)
	<-p.exit
	return p.waitErr
}

func receiveCancellationTest[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation test operation did not complete")
		var zero T
		return zero
	}
}

func TestProcessStartContextAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var launched, revoked bool
	p, err := startProcessContext(ctx, func() (managedProcess, error) {
		launched = true
		return nil, nil
	}, func() { revoked = true })
	if p != nil || !errors.Is(err, context.Canceled) || launched || !revoked {
		t.Fatalf("process=%v error=%v launched=%v revoked=%v", p, err, launched, revoked)
	}
}

func TestProcessStartContextReapsLateResult(t *testing.T) {
	for _, outcome := range []string{"approval", "denial", "handle-and-error", "cancel-at-return"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, approve := make(chan struct{}), make(chan struct{})
			child := newCancellationTestProcess()
			child.waitErr = errors.New("child exit status")
			var revoked atomic.Int32
			result := make(chan processStartResult, 1)
			go func() {
				p, err := startProcessContext(ctx, func() (managedProcess, error) {
					close(entered)
					<-approve
					if outcome == "cancel-at-return" {
						cancel()
					}
					if outcome == "denial" {
						return nil, errors.New("approval denied")
					}
					if outcome == "handle-and-error" {
						return child, errors.New("startup handshake failed")
					}
					return child, nil
				}, func() { revoked.Add(1) })
				result <- processStartResult{p, err}
			}()
			receiveCancellationTest(t, entered)
			if outcome == "cancel-at-return" {
				close(approve)
			} else {
				cancel()
			}
			started := receiveCancellationTest(t, result)
			if !errors.Is(started.err, context.Canceled) || started.proc == nil || started.proc.PID() != 0 || revoked.Load() != 1 {
				t.Fatalf("canceled launch: %+v revoked=%d", started, revoked.Load())
			}
			if outcome != "cancel-at-return" {
				// Cancellation returned before the uninterruptible call completed.
				close(approve)
			}
			if outcome != "denial" {
				receiveCancellationTest(t, child.killed)
				close(child.exit)
			}
			var waiters sync.WaitGroup
			for range 8 {
				waiters.Go(func() {
					err := started.proc.Wait()
					if outcome != "denial" && !errors.Is(err, child.waitErr) {
						t.Errorf("reaper wait: %v", err)
					}
				})
			}
			waiters.Wait()
			wantWaits := int32(1)
			if outcome == "denial" {
				wantWaits = 0
			}
			if child.waitCalls.Load() != wantWaits {
				t.Fatalf("child Wait calls: %d", child.waitCalls.Load())
			}
		})
	}
}

func TestProcessStartContextOwnsHandleOnStartupError(t *testing.T) {
	child := newCancellationTestProcess()
	launchErr := errors.New("PID was not published")
	var revoked atomic.Int32
	p, err := startProcessContext(context.Background(), func() (managedProcess, error) {
		return child, launchErr
	}, func() { revoked.Add(1) })
	if p == nil || !errors.Is(err, launchErr) || revoked.Load() != 1 {
		t.Fatalf("startup error: %v %v", p, err)
	}
	receiveCancellationTest(t, child.killed)
	close(child.exit)
	if err := p.Wait(); err != nil || child.waitCalls.Load() != 1 {
		t.Fatalf("cleanup wait: %v calls=%d", err, child.waitCalls.Load())
	}
}

func TestProcessStartContextDetachesSuccessfulProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child := newCancellationTestProcess()
	var revoked atomic.Int32
	p, err := startProcessContext(ctx, func() (managedProcess, error) { return child, nil }, func() { revoked.Add(1) })
	cancel()
	if p != child || err != nil || revoked.Load() != 0 || child.waitCalls.Load() != 0 {
		t.Fatalf("successful launch: %v %v revoked=%d", p, err, revoked.Load())
	}
	select {
	case <-child.killed:
		t.Fatal("canceling unary context killed an adopted process")
	default:
	}
	close(child.exit)
	_ = p.Wait()
}

func TestStartCoreProcessCanceledBeforeSpawn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, err := startCoreProcess(ctx, "must-not-execute", nil, t.TempDir(), io.Discard, false, nil)
	if p != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled start: %v %v", p, err)
	}
}

func TestProcessStartContextReapsRealLateChild(t *testing.T) {
	binary := helperBinary(t)
	root := t.TempDir()
	profile := testProfile(t, root, "check-wait")
	var output boundedOutput
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseLaunch := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseLaunch) // Never strand a real helper if an assertion fails.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan processStartResult, 1)
	go func() {
		proc, err := startProcessContext(ctx, func() (managedProcess, error) {
			child, err := startCoreProcess(context.Background(), binary, []string{"check", "-c", profile.Path}, root, &output, false, nil)
			if err != nil {
				return child, err
			}
			close(entered)
			<-release // OS created the child but has not returned its handle yet.
			return child, nil
		}, func() {})
		result <- processStartResult{proc, err}
	}()
	receiveCancellationTest(t, entered)
	waitUntil(t, func() bool { _, err := os.Stat(profile.Path + ".checking"); return err == nil })
	cancel()
	started := receiveCancellationTest(t, result)
	if started.proc == nil || !errors.Is(started.err, context.Canceled) {
		t.Fatalf("late child cancellation: %+v", started)
	}
	releaseLaunch()
	waitResult := make(chan error, 1)
	go func() { waitResult <- started.proc.Wait() }()
	if err := receiveCancellationTest(t, waitResult); err == nil {
		t.Fatal("blocking helper was not killed and reaped")
	}
}

func TestManagerCanceledLaunchRetainsOwnership(t *testing.T) {
	binary := helperBinary(t)
	for _, cancellation := range []string{"caller", "deadline", "stop", "denial-after-stop"} {
		t.Run(cancellation, func(t *testing.T) {
			root := t.TempDir()
			profile := testProfile(t, root, "")
			m := NewElevatedManager(root, nil)
			m.stopTimeout = time.Millisecond
			if cancellation == "deadline" {
				m.startTimeout = 700 * time.Millisecond
			}
			entered, approve := make(chan struct{}), make(chan struct{})
			child := newCancellationTestProcess()
			var revoked atomic.Bool
			m.startProcess = func(ctx context.Context, _ string, _ []string, _ string, _ io.Writer, elevated bool, _ []uint32) (managedProcess, error) {
				if !elevated {
					t.Error("elevation flag not forwarded")
				}
				return startProcessContext(ctx, func() (managedProcess, error) {
					close(entered)
					<-approve
					if cancellation == "denial-after-stop" {
						return nil, errors.New("UAC denied")
					}
					return child, nil
				}, func() { revoked.Store(true) })
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			startResult := make(chan error, 1)
			go func() { startResult <- m.Start(ctx, binary, profile, false) }()
			receiveCancellationTest(t, entered)
			m.mu.Lock()
			r := m.run
			dir := r.dir
			m.mu.Unlock()
			stopResult := make(chan error, 1)
			switch cancellation {
			case "caller":
				cancel()
			case "stop", "denial-after-stop":
				go func() { stopResult <- m.Stop(context.Background()) }()
			}
			err := receiveCancellationTest(t, startResult)
			want := context.Canceled
			if cancellation == "deadline" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || !revoked.Load() {
				t.Fatalf("start cancellation: %v revoked=%v", err, revoked.Load())
			}
			if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
				t.Fatalf("runtime directory removed before late handle arrived: %v", err)
			}
			if err := m.Start(context.Background(), binary, profile, false); !errors.Is(err, ErrBusy) {
				t.Fatalf("new launch accepted while old UAC outstanding: %v", err)
			}
			if cancellation != "caller" {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				if err := m.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Stop must wait for late launch cleanup: %v", err)
				}
				stopCancel()
			} else if s := m.State(); s.Phase != "error" || s.Error == "" {
				t.Fatalf("pending cancellation state: %+v", s)
			}
			select {
			case <-r.finished:
				t.Fatal("run finished before uninterruptible launch returned")
			default:
			}
			close(approve)
			if cancellation != "denial-after-stop" {
				receiveCancellationTest(t, child.killed)
				if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
					t.Fatalf("runtime directory removed before core/helper reaping: %v", err)
				}
				close(child.exit)
			}
			receiveCancellationTest(t, r.finished)
			if cancellation == "stop" || cancellation == "denial-after-stop" {
				if err := receiveCancellationTest(t, stopResult); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("runtime directory not cleaned after reaping: %v", err)
			}
			wantPhase := "stopped"
			if cancellation == "caller" {
				wantPhase = "error" // Cleanup must also finish without any Stop call.
			}
			if s := m.State(); s.Phase != wantPhase || s.PID != 0 || s.Secret != "" {
				t.Fatalf("post-cleanup state: %+v", s)
			}
			wantWaits := int32(1)
			if cancellation == "denial-after-stop" {
				wantWaits = 0
			}
			if child.waitCalls.Load() != wantWaits {
				t.Fatalf("Wait calls: %d", child.waitCalls.Load())
			}
			// A fresh launch uses its own process/capability. No late work from
			// the old launch may affect its state or runtime files.
			m.startProcess = startCoreProcess
			m.elevated = false
			cleanupManager(t, m)
			if err := m.Start(context.Background(), binary, profile, false); err != nil {
				t.Fatalf("restart after reaping: %v", err)
			}
		})
	}
}
