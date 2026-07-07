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

func TestManagerRollingUpdateSurgesBeforeRemovingDriftedContainer(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	runtime := testRuntime(ingressPort, servicePort)
	name := containerNameForDeployment(runtime, runtime.Spec.Deployments[0], 1)
	runner.addContainer(name, map[string]string{
		"aifar.runtime/managed":    "true",
		"aifar.runtime/namespace":  "prod",
		"aifar.runtime/name":       "demo",
		"aifar.runtime/deployment": "api",
		"aifar.runtime/replica":    "1",
		"aifar.runtime/revision":   "rev-1",
		"aifar.runtime/spec-hash":  "old",
	})

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.snapshotCalls(), "\n")
	runIndex := strings.Index(calls, "docker run ")
	rmIndex := strings.Index(calls, "docker rm -f "+name)
	renameIndex := strings.Index(calls, "docker rename "+name+"-surge-g")
	if runIndex < 0 || rmIndex < 0 || renameIndex < 0 {
		t.Fatalf("expected run, rm, and rename calls, got:\n%s", calls)
	}
	if !(runIndex < rmIndex && rmIndex < renameIndex) {
		t.Fatalf("expected surge run before rm before rename, got:\n%s", calls)
	}
	if err := manager.Remove(context.Background(), "prod", "demo"); err != nil {
		t.Fatal(err)
	}
}
