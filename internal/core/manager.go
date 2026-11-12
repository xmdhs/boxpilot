package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

type managedRun struct {
	proc                      managedProcess
	output                    boundedOutput
	secret                    string
	dir                       string
	cancel                    context.CancelFunc
	exited                    chan struct{}
	finished                  chan struct{}
	waitErr                   error
	cleanupErr                error
	ready, stopping, complete bool
	terminate                 sync.Once
}

type Manager struct {
	root       string
	mu         sync.Mutex
	state      State
	run        *managedRun
	publish    func(State)
	pending    []State
	publishing bool
	// Kept separate from process control for deterministic offline tests.
	probe        func(context.Context, string, string) error
	startTimeout time.Duration
	stopTimeout  time.Duration
	elevated     bool
	startProcess processStarter
}

func NewManager(root string, publish func(State)) *Manager {
	return newManager(root, publish, false)
}

func NewElevatedManager(root string, publish func(State)) *Manager {
	return newManager(root, publish, true)
}

func newManager(root string, publish func(State), elevated bool) *Manager {
	return &Manager{root: root, publish: publish, state: State{Phase: "stopped"}, probe: probeAPI, startTimeout: 30 * time.Second, stopTimeout: 3 * time.Second, elevated: elevated, startProcess: startCoreProcess}
}
func (m *Manager) snapshotLocked() State {
	s := m.state
	if m.run != nil {
		s.Output = redact(m.run.output.String(), m.run.secret)
		s.Error = redact(s.Error, m.run.secret)
	}
	return s
}
func (m *Manager) notifyLocked() {
	if m.publish == nil {
		return
	}
	m.pending = append(m.pending, m.snapshotLocked())
	if !m.publishing {
		m.publishing = true
		go m.deliver()
	}
}
func (m *Manager) deliver() {
	for {
		m.mu.Lock()
		if len(m.pending) == 0 {
			m.publishing = false
			m.mu.Unlock()
			return
		}
		s := m.pending[0]
		m.pending[0] = State{}
		m.pending = m.pending[1:]
		m.mu.Unlock()
		// No operation or state lock is held; callbacks may safely call State/Stop.
		m.publish(s)
	}
}
func (m *Manager) State() State  { m.mu.Lock(); defer m.mu.Unlock(); return m.snapshotLocked() }
func (m *Manager) Running() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.state.Phase == "running" }

func (m *Manager) SetCPUAffinity(ctx context.Context, ids []uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	r := m.run
	if r == nil || !r.ready || r.proc == nil || m.state.Phase != "running" {
		m.mu.Unlock()
		return ErrNotRunning
	}
	proc := r.proc
	pid := proc.PID()
	m.mu.Unlock()
	ids = append([]uint32(nil), ids...)
	if setter, ok := proc.(interface{ SetCPUAffinity([]uint32) error }); ok {
		return setter.SetCPUAffinity(ids)
	}
	return setProcessCPUAffinity(pid, ids)
}
func (m *Manager) finish(r *managedRun, err error) {
	cleanupErr := cleanupRunDirectory(r.dir)
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.complete {
		return
	}
	r.complete = true
	r.cleanupErr = cleanupErr
	err = errors.Join(err, cleanupErr)
	phase := "error"
	message := ""
	if err != nil {
		message = redact(err.Error(), r.secret)
	}
	if r.stopping {
		phase = "stopped"
		message = ""
		if cleanupErr != nil {
			phase = "error"
			message = redact(cleanupErr.Error(), r.secret)
		}
	}
	m.state = State{Phase: phase, Error: message, Version: m.state.Version, Output: redact(r.output.String(), r.secret)}
	m.run = nil
	close(r.finished)
	m.notifyLocked()
}
func (m *Manager) terminateRun(r *managedRun) {
	m.mu.Lock()
	proc := r.proc
	m.mu.Unlock()
	if proc == nil {
		return
	}
	r.terminate.Do(func() {
		go func() {
			// Wait is owned exclusively by the waiter goroutine, never by Stop.
			select {
			case <-r.exited:
				return
			default:
			}
			if err := proc.GracefulStop(); err != nil {
				// 提权 helper 无法接收控制台信号，立即触发取消事件，
				// 不让停止或取消继续等待整个优雅退出超时。
				_ = proc.Kill()
				return
			}
			timer := time.NewTimer(m.stopTimeout)
			defer timer.Stop()
			select {
			case <-r.exited:
				return
			case <-timer.C:
				_ = proc.Kill()
			}
		}()
	})
}
func (m *Manager) Start(ctx context.Context, binary string, p Profile, allowAPIReplace bool, cpuSetChoices ...[]uint32) error {
	var startupCPUSetIDs []uint32
	if len(cpuSetChoices) > 0 {
		startupCPUSetIDs = append([]uint32(nil), cpuSetChoices[0]...)
	}
	startup, cancel := context.WithTimeout(ctx, m.startTimeout)
	r := &managedRun{cancel: cancel, exited: make(chan struct{}), finished: make(chan struct{})}
	m.mu.Lock()
	if m.run != nil {
		m.mu.Unlock()
		cancel()
		return ErrBusy
	}
	m.run = r
	m.state = State{Phase: "checking"}
	m.notifyLocked()
	m.mu.Unlock()
	defer cancel()
	fail := func(err error) error {
		m.mu.Lock()
		spawned := r.proc != nil
		m.mu.Unlock()
		if spawned {
			m.terminateRun(r)
			<-r.exited
		}
		m.finish(r, err)
		return err
	}
	installed, err := Inspect(startup, binary)
	if err != nil {
		return fail(err)
	}
	m.mu.Lock()
	m.state.Version = installed.Version
	m.mu.Unlock()
	content, err := ReadProfile(p)
	if err != nil {
		return fail(err)
	}
	secret, err := randomID()
	if err != nil {
		return fail(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fail(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	derived, err := deriveConfig([]byte(content), port, secret, allowAPIReplace)
	if err != nil {
		return fail(err)
	}
	dir, config, err := prepareRunConfig(m.root, p.Path, derived)
	if err != nil {
		return fail(err)
	}
	r.dir = dir
	m.mu.Lock()
	r.secret = secret
	m.mu.Unlock()
	if err = atomicWrite(config, derived); err != nil {
		return fail(err)
	}
	output, err := Check(startup, installed.Path, config)
	r.output.Write([]byte(output))
	if err != nil {
		return fail(fmt.Errorf("%w: %s", err, redact(output, secret)))
	}
	if err = startup.Err(); err != nil {
		return fail(err)
	}
	proc, err := m.startProcess(startup, installed.Path, []string{"run", "-c", config}, r.dir, &r.output, m.elevated, startupCPUSetIDs)
	if proc == nil {
		return fail(err)
	}
	m.mu.Lock()
	r.proc = proc
	m.mu.Unlock()
	go func() {
		// 启动失败后仍可能持有未返回的 ShellExecute 调用，
		// 此处也是迟到 sing-box 句柄的唯一等待者。
		err := proc.Wait()
		m.mu.Lock()
		r.waitErr = err
		close(r.exited)
		ready := r.ready
		m.mu.Unlock()
		if ready {
			if err == nil {
				err = errors.New("core exited unexpectedly")
			}
			m.finish(r, err)
		}
	}()
	if err != nil {
		// 及时返回取消错误，但在迟到进程被终止并回收前保留运行目录和忙碌状态。
		// Stop 等待 finished，而不是等待已经取消的 Start 调用方。
		m.mu.Lock()
		if !r.stopping {
			m.state.Phase = "error"
			m.state.Error = redact(err.Error(), secret)
			m.notifyLocked()
		}
		m.mu.Unlock()
		go func() {
			m.terminateRun(r)
			<-r.exited
			m.finish(r, err)
		}()
		return err
	}
	m.mu.Lock()
	if startup.Err() != nil || r.stopping {
		m.mu.Unlock()
		_ = proc.Kill()
		return fail(contextError(startup, context.Canceled))
	}
	m.state.Phase = "starting"
	m.state.PID = proc.PID()
	m.notifyLocked()
	m.mu.Unlock()
	if len(startupCPUSetIDs) > 0 {
		var applyErr error
		if setter, ok := proc.(interface{ SetCPUAffinity([]uint32) error }); ok {
			applyErr = setter.SetCPUAffinity(startupCPUSetIDs)
		} else {
			applyErr = setProcessCPUAffinity(proc.PID(), startupCPUSetIDs)
		}
		if err := applyErr; err != nil {
			return fail(fmt.Errorf("apply CPU scheduling to managed sing-box: %w", err))
		}
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	var probeErr error
	for {
		select {
		case <-startup.Done():
			err := startup.Err()
			if probeErr != nil {
				err = fmt.Errorf("API readiness: %w (%s)", err, redact(probeErr.Error(), secret))
			}
			return fail(err)
		case <-r.exited:
			m.mu.Lock()
			err := r.waitErr
			m.mu.Unlock()
			if err == nil {
				err = errors.New("core exited before API readiness")
			}
			return fail(err)
		default:
		}
		attempt, attemptCancel := context.WithTimeout(startup, 1500*time.Millisecond)
		probeErr = m.probe(attempt, endpoint, secret)
		attemptCancel()
		if probeErr == nil {
			m.mu.Lock()
			select {
			case <-r.exited:
				m.mu.Unlock()
				return fail(errors.New("core exited before API readiness"))
			default:
			}
			if startup.Err() != nil || r.stopping {
				m.mu.Unlock()
				return fail(context.Canceled)
			}
			r.ready = true
			m.state.Phase = "running"
			m.state.URL = endpoint
			m.state.Secret = secret
			m.notifyLocked()
			m.mu.Unlock()
			// The process has no relationship to the caller's unary context after readiness.
			return nil
		}
		select {
		case <-startup.Done():
			continue
		case <-r.exited:
			continue
		case <-ticker.C:
		}
	}
}
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	r := m.run
	if r == nil {
		m.mu.Unlock()
		return nil
	}
	r.stopping = true
	r.cancel()
	m.state.Phase = "stopping"
	m.notifyLocked()
	m.mu.Unlock()
	// Termination/reaping continues even when the caller's shutdown deadline expires.
	m.terminateRun(r)
	select {
	case <-r.finished:
		m.mu.Lock()
		err := r.cleanupErr
		m.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
