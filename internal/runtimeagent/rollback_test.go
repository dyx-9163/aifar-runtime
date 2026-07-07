package runtimeagent

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestManagerRollingUpdateFailureRollsBackToPreviousRuntime(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	config := DefaultRuntimeConfig()
	config.Container.ReadyTimeout = Duration{Duration: 2 * time.Millisecond}
	config.Container.ReadyPollInterval = Duration{Duration: time.Millisecond}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner, Config: config})
	defer manager.Shutdown(context.Background())

	runtime := testRuntime(ingressPort, servicePort)
	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	previous, previousStatus, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("previous runtime not found")
	}
	oldName := containerNameForDeployment(previous, previous.Spec.Deployments[0], 1)

	updated := previous
	updated.Metadata.Generation = 0
	updated.Spec.Deployments[0].Image = "demo-api:2"
	updated.Spec.Deployments[0].Revision = "rev-2"
	newName := containerNameForDeployment(updated, updated.Spec.Deployments[0], 1)
	runner.failReadinessForName(newName)

	err := manager.Apply(context.Background(), updated)
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected rollback error, got %v", err)
	}
	restored, status, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("restored runtime not found")
	}
	if restored.Spec.Deployments[0].Revision != "rev-1" || restored.Metadata.Generation != previous.Metadata.Generation {
		t.Fatalf("expected previous runtime to be restored, got %#v", restored)
	}
	if status.Phase != previousStatus.Phase {
		t.Fatalf("expected previous status to be restored, got %#v", status)
	}
	if !runner.hasContainer(oldName) {
		t.Fatalf("expected old container %s to remain", oldName)
	}
	if runner.hasContainer(newName) {
		t.Fatalf("expected failed new container %s to be removed", newName)
	}
	events, err := manager.Events("prod", "demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	reasons := eventReasons(events)
	if !strings.Contains(reasons, "RollbackStarted") || !strings.Contains(reasons, "RollbackComplete") {
		t.Fatalf("expected rollback events, got %#v", events)
	}
}

func TestManagerSurgeFailureKeepsCanonicalContainer(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	config := DefaultRuntimeConfig()
	config.Container.ReadyTimeout = Duration{Duration: 2 * time.Millisecond}
	config.Container.ReadyPollInterval = Duration{Duration: time.Millisecond}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner, Config: config})
	defer manager.Shutdown(context.Background())

	runtime := testRuntime(ingressPort, servicePort)
	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	previous, _, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("previous runtime not found")
	}
	canonicalName := containerNameForDeployment(previous, previous.Spec.Deployments[0], 1)

	updated := previous
	updated.Metadata.Generation = 0
	updated.Spec.Deployments[0].Env = map[string]string{"VERSION": "same-revision-drift"}
	failedGeneration := previous.Metadata.Generation + 1
	surgeName := sanitizeDockerName(canonicalName + "-surge-g" + strconvInt64(failedGeneration))
	runner.failReadinessForName(surgeName)

	err := manager.Apply(context.Background(), updated)
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected rollback error, got %v", err)
	}
	restored, _, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("restored runtime not found")
	}
	if len(restored.Spec.Deployments[0].Env) != 0 {
		t.Fatalf("expected drifted spec to be rolled back, got %#v", restored.Spec.Deployments[0].Env)
	}
	if !runner.hasContainer(canonicalName) {
		t.Fatalf("expected canonical container %s to remain", canonicalName)
	}
	if runner.hasContainer(surgeName) {
		t.Fatalf("expected failed surge container %s to be removed", surgeName)
	}
	for _, call := range runner.snapshotCalls() {
		if call == "docker rm -f "+canonicalName {
			t.Fatalf("canonical container should not be removed on surge readiness failure, got:\n%s", strings.Join(runner.snapshotCalls(), "\n"))
		}
	}
}

func eventReasons(events []RuntimeEvent) string {
	reasons := make([]string, 0, len(events))
	for _, event := range events {
		reasons = append(reasons, event.Reason)
	}
	return strings.Join(reasons, ",")
}

func strconvInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
