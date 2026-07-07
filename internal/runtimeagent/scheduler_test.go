package runtimeagent

import (
	"context"
	"strings"
	"testing"
)

func TestSchedulerAssignsRuntimeToLocalNode(t *testing.T) {
	config := DefaultRuntimeConfig()
	config.Node.Name = "edge-a"
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner(), Config: config})
	runtime := testRuntime(freePort(t), freePort(t))

	admitted, err := manager.AdmitRuntime(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Spec.NodeName != "edge-a" {
		t.Fatalf("expected node assignment edge-a, got %#v", admitted.Spec)
	}
}

func TestSchedulerRejectsGlobalServiceListenPortConflict(t *testing.T) {
	servicePort := freePort(t)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner()})
	defer manager.Shutdown(context.Background())
	first := testRuntime(freePort(t), servicePort)
	if err := manager.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := runtimeWithName(testRuntime(freePort(t), servicePort), "demo-two")

	err := manager.Apply(context.Background(), second)
	if err == nil || !strings.Contains(err.Error(), "listenPort") {
		t.Fatalf("expected global listenPort conflict, got %v", err)
	}
	if _, _, found := manager.GetRuntime("prod", "demo-two"); found {
		t.Fatal("rejected runtime must not be stored")
	}
}

func TestSchedulerAllowsSharedIngressPortWithDifferentHostPath(t *testing.T) {
	ingressPort := freePort(t)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner()})
	defer manager.Shutdown(context.Background())
	first := testRuntime(ingressPort, freePort(t))
	first.Spec.Ingress[0].Host = "api.example.test"
	if err := manager.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := runtimeWithName(testRuntime(ingressPort, freePort(t)), "demo-two")
	second.Spec.Ingress[0].Host = "web.example.test"

	if err := manager.Apply(context.Background(), second); err != nil {
		t.Fatalf("expected shared ingress port with different host to pass, got %v", err)
	}
	if err := manager.Remove(context.Background(), "prod", "demo"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), "prod", "demo-two"); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerRejectsGlobalIngressHostPathConflict(t *testing.T) {
	ingressPort := freePort(t)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner()})
	defer manager.Shutdown(context.Background())
	first := testRuntime(ingressPort, freePort(t))
	first.Spec.Ingress[0].Host = "api.example.test"
	if err := manager.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := runtimeWithName(testRuntime(ingressPort, freePort(t)), "demo-two")
	second.Spec.Ingress[0].Host = "api.example.test"

	err := manager.Apply(context.Background(), second)
	if err == nil || !strings.Contains(err.Error(), "conflicts with ingress") {
		t.Fatalf("expected ingress host/path conflict, got %v", err)
	}
}

func TestSchedulerRejectsNodeCapacityOverflow(t *testing.T) {
	config := DefaultRuntimeConfig()
	config.Node.Capacity = ResourceSpec{CPUs: "1", Memory: "512Mi", PIDsLimit: 128}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner(), Config: config})
	defer manager.Shutdown(context.Background())
	first := testRuntime(freePort(t), freePort(t))
	first.Spec.Deployments[0].Resources = ResourceSpec{CPUs: "0.6", Memory: "256Mi", PIDsLimit: 64}
	if err := manager.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := runtimeWithName(testRuntime(freePort(t), freePort(t)), "demo-two")
	second.Spec.Deployments[0].Resources = ResourceSpec{CPUs: "0.5", Memory: "256Mi", PIDsLimit: 64}

	err := manager.Apply(context.Background(), second)
	if err == nil || !strings.Contains(err.Error(), "CPU capacity exceeded") {
		t.Fatalf("expected CPU capacity error, got %v", err)
	}
}

func TestSchedulerResourceSnapshotReportsRequestedAndAvailable(t *testing.T) {
	config := DefaultRuntimeConfig()
	config.Node.Capacity = ResourceSpec{CPUs: "2", Memory: "1Gi", PIDsLimit: 256}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner(), Config: config})
	defer manager.Shutdown(context.Background())
	runtime := testRuntime(freePort(t), freePort(t))
	runtime.Spec.Deployments[0].Resources = ResourceSpec{CPUs: "0.5", Memory: "256Mi", PIDsLimit: 32}
	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}

	snapshot := manager.ResourceSnapshot()
	if snapshot.Requested.MilliCPUs != 500 {
		t.Fatalf("expected 500m CPU requested, got %#v", snapshot)
	}
	if snapshot.Requested.MemoryBytes != 256*1024*1024 {
		t.Fatalf("expected 256Mi requested, got %#v", snapshot)
	}
	if snapshot.Available.MilliCPUs != 1500 || snapshot.Available.PIDs != 224 {
		t.Fatalf("unexpected available resources: %#v", snapshot)
	}
	status := manager.Status()
	if _, ok := status["scheduler"].(ResourceSnapshot); !ok {
		t.Fatalf("expected scheduler snapshot in status, got %#v", status["scheduler"])
	}
}

func TestParseResourceQuantities(t *testing.T) {
	cpu, err := parseCPUMilli("0.0001")
	if err != nil {
		t.Fatal(err)
	}
	if cpu != 1 {
		t.Fatalf("expected CPU to round up to 1m, got %d", cpu)
	}
	memory, err := parseMemoryBytes("2Gi")
	if err != nil {
		t.Fatal(err)
	}
	if memory != 2*1024*1024*1024 {
		t.Fatalf("expected 2Gi bytes, got %d", memory)
	}
}

func runtimeWithName(runtime Runtime, name string) Runtime {
	runtime.Metadata.Name = name
	return NormalizeRuntime(runtime)
}
