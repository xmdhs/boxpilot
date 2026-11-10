package desktop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ErrTrayHostUnavailable means no usable StatusNotifierWatcher/host could be
// verified. Treat it as a warning, never as permission to hide the window.
var ErrTrayHostUnavailable = fmt.Errorf("no usable StatusNotifier tray host: %w", errors.ErrUnsupported)

// ProbeTrayHost verifies Linux's session-bus watcher AND its host-registered
// property. A bus name alone does not guarantee that an icon can be recovered.
// It runs only fixed gdbus/dbus-send commands with an overall 1.5s deadline and
// short per-command deadlines. Call before App.Run or on a worker, NOT from a
// UI callback; deliver the result with Controller.SetTrayHostProbe on main.
// macOS/Windows do not need a bus and return nil without launching a process.
func ProbeTrayHost(ctx context.Context) error {
	return probeTrayHost(ctx, runtime.GOOS, exec.LookPath, func(ctx context.Context, path string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.WaitDelay = 100 * time.Millisecond
		return cmd.Output()
	})
}

type probeRunner func(context.Context, string, ...string) ([]byte, error)

func probeTrayHost(ctx context.Context, platform string, lookup func(string) (string, error), run probeRunner) error {
	if platform != "linux" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var failures []error
	for _, tool := range []string{"gdbus", "dbus-send"} {
		path, err := lookup(tool)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", tool, err))
			continue
		}
		for _, namespace := range []string{"org.kde", "org.freedesktop"} {
			if err := ctx.Err(); err != nil {
				return errors.Join(ErrTrayHostUnavailable, err)
			}
			watcher := namespace + ".StatusNotifierWatcher"
			var args []string
			if tool == "gdbus" {
				args = []string{"call", "--session", "--dest", watcher, "--object-path", "/StatusNotifierWatcher", "--method", "org.freedesktop.DBus.Properties.Get", watcher, "IsStatusNotifierHostRegistered"}
			} else {
				args = []string{"--session", "--print-reply", "--reply-timeout=350", "--dest=" + watcher, "/StatusNotifierWatcher", "org.freedesktop.DBus.Properties.Get", "string:" + watcher, "string:IsStatusNotifierHostRegistered"}
			}
			queryCtx, stop := context.WithTimeout(ctx, 400*time.Millisecond)
			out, err := run(queryCtx, path, args...)
			stop()
			if err == nil && hostRegistered(string(out)) {
				return nil
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("%s %s: %w", tool, watcher, err))
			}
		}
	}
	if ctx.Err() != nil {
		failures = append(failures, ctx.Err())
	}
	return errors.Join(append([]error{ErrTrayHostUnavailable}, failures...)...)
}

func hostRegistered(output string) bool {
	// gdbus: (<true>,); dbus-send: ... variant boolean true. Match a
	// complete boolean token, not an incidental substring such as "untrue".
	output = strings.NewReplacer("<", " ", ">", " ", "(", " ", ")", " ", ",", " ").Replace(output)
	found := false
	for _, token := range strings.Fields(output) {
		if token == "false" {
			return false
		}
		if token == "true" {
			found = true
		}
	}
	return found
}
