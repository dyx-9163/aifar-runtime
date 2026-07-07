package runtimeagent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestManagerReplicasZeroKeepsServiceListenerWithoutContainerRun(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	runtime := testRuntime(ingressPort, servicePort)
	zero := 0
	runtime.Spec.Deployments[0].Replicas = &zero

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if got := runner.countCalls("docker run "); got != 0 {
		t.Fatalf("expected replicas=0 to avoid docker run, got %d", got)
	}
	_, status, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("runtime status not found")
	}
	if len(status.Services) != 1 || len(status.Services[0].Endpoints) != 0 {
		t.Fatalf("expected empty service endpoints, got %#v", status.Services)
	}
	if err := manager.Remove(context.Background(), "prod", "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerApplyReturnsDockerListErrorsWhenCleaningReplicas(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: failingDockerRunner{
		failNeedle: "docker ps -a",
		err:        errors.New("docker ps failed"),
	}})
	runtime := testRuntime(ingressPort, servicePort)

	err := manager.Apply(context.Background(), runtime)
	if err == nil || !strings.Contains(err.Error(), "list extra AIFAR pods") {
		t.Fatalf("expected docker ps cleanup error, got %v", err)
	}
}
