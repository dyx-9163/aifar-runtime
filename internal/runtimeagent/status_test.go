package runtimeagent

import (
	"errors"
	"testing"
)

func TestNewStatusMarksSubresourcesFailed(t *testing.T) {
	runtime := testRuntime(18080, 19000)
	status := NewStatus(runtime, "Failed", nil, errors.New("boom"))

	if status.Phase != "Failed" {
		t.Fatalf("expected failed runtime phase, got %q", status.Phase)
	}
	if status.Deployments[0].Phase != "Failed" || status.Services[0].Phase != "Failed" || status.Ingress[0].Phase != "Failed" {
		t.Fatalf("expected failed subresource phases, got %#v", status)
	}
}

func TestNewStatusCountsReadyDeploymentReplicas(t *testing.T) {
	runtime := testRuntime(18080, 19000)
	container := containerNameForDeployment(runtime, runtime.Spec.Deployments[0], 1)
	status := NewStatus(runtime, "Ready", map[string][]Endpoint{
		"api": {{Container: container, Address: "172.20.0.10:9000"}},
	}, nil)

	if status.Deployments[0].Ready != 1 {
		t.Fatalf("expected one ready replica, got %#v", status.Deployments[0])
	}
}
