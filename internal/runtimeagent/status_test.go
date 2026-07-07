package runtimeagent

import (
	"errors"
	"testing"
)

func TestNewStatusMarksSubresourcesFailed(t *testing.T) {
	runtime := testRuntime(18080, 19000)
	status := NewStatus(runtime, RuntimePhaseFailed, nil, errors.New("boom"))

	if status.Phase != RuntimePhaseFailed {
		t.Fatalf("expected failed runtime phase, got %q", status.Phase)
	}
	if status.Deployments[0].Phase != RuntimePhaseFailed || status.Services[0].Phase != RuntimePhaseFailed || status.Ingress[0].Phase != RuntimePhaseFailed {
		t.Fatalf("expected failed subresource phases, got %#v", status)
	}
}

func TestNewStatusCountsReadyDeploymentReplicas(t *testing.T) {
	runtime := testRuntime(18080, 19000)
	container := containerNameForDeployment(runtime, runtime.Spec.Deployments[0], 1)
	status := NewStatus(runtime, RuntimePhaseRunning, map[string][]Endpoint{
		"api": {{Container: container, Address: "172.20.0.10:9000"}},
	}, nil)

	if status.Phase != RuntimePhaseRunning {
		t.Fatalf("expected running runtime phase, got %#v", status)
	}
	if status.Deployments[0].Ready != 1 {
		t.Fatalf("expected one ready replica, got %#v", status.Deployments[0])
	}
}

func TestNewStatusMarksMissingReadyReplicasDegraded(t *testing.T) {
	runtime := testRuntime(18080, 19000)
	status := NewStatus(runtime, RuntimePhaseRunning, nil, nil)

	if status.Phase != RuntimePhaseDegraded {
		t.Fatalf("expected degraded phase, got %#v", status)
	}
	if status.Deployments[0].Phase != RuntimePhaseDegraded {
		t.Fatalf("expected degraded deployment, got %#v", status.Deployments[0])
	}
}
