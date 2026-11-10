package desktop

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestProbeNonLinuxDoesNotNeedBus(t *testing.T) {
	for _, platform := range []string{"windows", "darwin"} {
		err := probeTrayHost(context.Background(), platform, func(string) (string, error) {
			t.Fatal("non-Linux probe looked for a D-Bus tool")
			return "", nil
		}, func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("non-Linux probe launched a process")
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestProbeRequiresHostRegisteredNotJustWatcher(t *testing.T) {
	for _, output := range []string{"", "(<false>,)", "org.kde.StatusNotifierWatcher", "untrue"} {
		calls := 0
		err := probeTrayHost(context.Background(), "linux", func(tool string) (string, error) { return tool, nil }, func(ctx context.Context, tool string, args ...string) ([]byte, error) {
			calls++
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("probe has no deadline")
			}
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "IsStatusNotifierHostRegistered") || !strings.Contains(joined, "/StatusNotifierWatcher") || !strings.Contains(joined, "--session") {
				t.Fatalf("wrong probe arguments: %q", args)
			}
			return []byte(output), nil
		})
		if !errors.Is(err, ErrTrayHostUnavailable) || !errors.Is(err, errors.ErrUnsupported) || calls != 4 {
			t.Fatalf("output %q: error=%v calls=%d", output, err, calls)
		}
	}
}

func TestProbeSupportsToolsAndWatcherNamespaces(t *testing.T) {
	for _, tool := range []string{"gdbus", "dbus-send"} {
		for _, namespace := range []string{"org.kde", "org.freedesktop"} {
			err := probeTrayHost(context.Background(), "linux", func(name string) (string, error) {
				if name != tool {
					return "", exec.ErrNotFound
				}
				return name, nil
			}, func(_ context.Context, path string, args ...string) ([]byte, error) {
				if !strings.Contains(strings.Join(args, " "), namespace+".StatusNotifierWatcher") {
					return nil, errors.New("watcher not present")
				}
				if path == "gdbus" {
					return []byte("(<true>,)\n"), nil
				}
				return []byte("method return time=0 sender=:1.0 -> destination=:1.1\n   variant       boolean true\n"), nil
			})
			if err != nil {
				t.Fatalf("%s %s: %v", tool, namespace, err)
			}
		}
	}
}

func TestProbeCancellationAndMissingTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := probeTrayHost(ctx, "linux", func(tool string) (string, error) { return tool, nil }, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("cancelled probe launched process")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrTrayHostUnavailable) {
		t.Fatal(err)
	}
	err = probeTrayHost(context.Background(), "linux", func(string) (string, error) { return "", exec.ErrNotFound }, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("probe launched absent tool")
		return nil, nil
	})
	if !errors.Is(err, ErrTrayHostUnavailable) || !errors.Is(err, exec.ErrNotFound) {
		t.Fatal(err)
	}
	// Deterministically check cancellation during a runner, without asserting
	// elapsed time or requiring a real bus or desktop session.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err = probeTrayHost(ctx, "linux", func(tool string) (string, error) { return tool, nil }, func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		calls++
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("probe continued after cancellation: calls=%d, %v", calls, err)
	}
}

func TestHostRegisteredTokens(t *testing.T) {
	for _, output := range []string{"(<true>,)", "variant boolean true", "( < true > , )"} {
		if !hostRegistered(output) {
			t.Fatalf("true not recognized: %q", output)
		}
	}
	for _, output := range []string{"(<false>,)", "untrue", "true-ish", "true false", ""} {
		if hostRegistered(output) {
			t.Fatalf("false positive: %q", output)
		}
	}
}
