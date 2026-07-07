package runtimeagent

import (
	"context"
	"testing"
)

func TestManagerMetricsReportsRuntimeState(t *testing.T) {
	runner := newFakeDockerRunner()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	defer manager.Shutdown(context.Background())
	runtime := testRuntime(freePort(t), freePort(t))

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}

	metrics := manager.Metrics()
	if metrics.RuntimeCount != 1 {
		t.Fatalf("runtime count = %d", metrics.RuntimeCount)
	}
	if metrics.ListenerCount != 2 {
		t.Fatalf("listener count = %d", metrics.ListenerCount)
	}
	if metrics.DesiredReplicas != 1 || metrics.ReadyReplicas != 1 {
		t.Fatalf("unexpected replicas: desired=%d ready=%d", metrics.DesiredReplicas, metrics.ReadyReplicas)
	}
	if metrics.EndpointCount != 1 {
		t.Fatalf("endpoint count = %d", metrics.EndpointCount)
	}
}
