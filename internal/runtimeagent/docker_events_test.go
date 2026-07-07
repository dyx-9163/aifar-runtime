package runtimeagent

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestManagerDockerEventWatcherIsInjectable(t *testing.T) {
	called := false
	manager := NewManager(ManagerOptions{
		StateDir: t.TempDir(),
		Runner:   newFakeDockerRunner(),
		DockerEvents: func(ctx context.Context) (io.ReadCloser, io.ReadCloser, func() error, error) {
			called = true
			return io.NopCloser(strings.NewReader("1 start demo\n")), io.NopCloser(strings.NewReader("")), func() error { return nil }, nil
		},
	})

	if err := manager.watchDockerEvents(context.Background(), time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected injected Docker event watcher to be used")
	}
}
