package runtimeagent

import (
	"context"
	"strings"
	"testing"
)

func TestManagerRejectsRuntimeAssignedToDifferentNode(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	config := DefaultRuntimeConfig()
	config.Node.Name = "edge-a"
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner(), Config: config})
	runtime := testRuntime(ingressPort, servicePort)
	runtime.Spec.NodeName = "edge-b"

	err := manager.Apply(context.Background(), runtime)
	if err == nil || !strings.Contains(err.Error(), "current node") {
		t.Fatalf("expected node assignment rejection, got %v", err)
	}
	if _, _, found := manager.GetRuntime("prod", "demo"); found {
		t.Fatal("runtime assigned to a different node must not be stored locally")
	}
}

func TestManagerStatusIncludesLocalNode(t *testing.T) {
	config := DefaultRuntimeConfig()
	config.Node.Name = "edge-a"
	config.Node.Labels = map[string]string{"zone": "lab"}
	config.Node.Capacity = ResourceSpec{CPUs: "2", Memory: "2Gi"}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner(), Config: config})

	status := manager.Status()
	node, ok := status["node"].(NodeStatus)
	if !ok {
		t.Fatalf("expected NodeStatus, got %#v", status["node"])
	}
	if node.Name != "edge-a" || node.Labels["zone"] != "lab" || !node.Ready {
		t.Fatalf("unexpected node status: %#v", node)
	}
	if node.Allocatable.Memory != "2Gi" {
		t.Fatalf("expected allocatable to default to capacity, got %#v", node.Allocatable)
	}
}
