package runtimeagent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestManagerResyncRestartsExitedContainer(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	config := DefaultRuntimeConfig()
	config.SelfHeal.Backoff = Duration{Duration: time.Millisecond}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner, Config: config})
	runtime := testRuntime(ingressPort, servicePort)

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	name := containerNameForDeployment(runtime, runtime.Spec.Deployments[0], 1)
	runner.setContainerState(name, false, "exited", 137, "")

	if err := manager.Resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.snapshotCalls(), "\n")
	if !strings.Contains(calls, "docker rm -f "+name) {
		t.Fatalf("expected self-heal to remove exited container, got:\n%s", calls)
	}
	if got := runner.countCalls("docker run "); got != 2 {
		t.Fatalf("expected initial run and self-heal run, got %d calls:\n%s", got, calls)
	}
	_, status, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("runtime status not found")
	}
	if status.Phase != RuntimePhaseRunning || status.Deployments[0].Restarts != 1 {
		t.Fatalf("expected running status with one restart, got %#v", status)
	}
}

func TestManagerSelfHealHonorsRestartLimit(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	config := DefaultRuntimeConfig()
	config.SelfHeal.MaxRestarts = 1
	config.SelfHeal.Backoff = Duration{Duration: time.Millisecond}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner, Config: config})
	runtime := testRuntime(ingressPort, servicePort)

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	name := containerNameForDeployment(runtime, runtime.Spec.Deployments[0], 1)
	runner.setContainerState(name, false, "exited", 1, "")
	if err := manager.Resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.setContainerState(name, false, "exited", 1, "")
	time.Sleep(2 * time.Millisecond)
	if err := manager.Resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := runner.countCalls("docker run "); got != 2 {
		t.Fatalf("expected restart limit to stop second self-heal run, got %d calls:\n%s", got, strings.Join(runner.snapshotCalls(), "\n"))
	}
	_, status, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("runtime status not found")
	}
	if status.Phase != RuntimePhaseDegraded {
		t.Fatalf("expected degraded status after restart limit, got %#v", status)
	}
	if !strings.Contains(status.Deployments[0].Message, "restart limit exceeded") {
		t.Fatalf("expected restart limit message, got %#v", status.Deployments[0])
	}
}
