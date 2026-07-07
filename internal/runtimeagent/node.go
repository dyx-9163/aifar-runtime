package runtimeagent

import (
	"fmt"
	"strings"
	"time"
)

func (m *Manager) nodeStatus() NodeStatus {
	labels := copyStringMap(m.config.Node.Labels)
	allocatable := m.config.Node.Allocatable
	if resourceSpecEmpty(allocatable) {
		allocatable = m.config.Node.Capacity
	}
	return NodeStatus{
		Name:              m.config.Node.Name,
		Labels:            labels,
		Capacity:          m.config.Node.Capacity,
		Allocatable:       allocatable,
		Ready:             true,
		LastHeartbeatTime: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func (m *Manager) validateRuntimeNode(runtime Runtime) error {
	runtime = NormalizeRuntime(runtime)
	node := m.config.Node
	if runtime.Spec.NodeName != "" && runtime.Spec.NodeName != node.Name {
		return fmt.Errorf("runtime %s is assigned to nodeName %q, current node is %q", KeyForRuntime(runtime).String(), runtime.Spec.NodeName, node.Name)
	}
	for key, want := range runtime.Spec.NodeSelector {
		got := strings.TrimSpace(node.Labels[key])
		if got != strings.TrimSpace(want) {
			return fmt.Errorf("runtime %s nodeSelector %s=%s does not match current node value %q", KeyForRuntime(runtime).String(), key, want, got)
		}
	}
	return nil
}

func (m *Manager) statusOptions(runtime Runtime) StatusOptions {
	return StatusOptions{
		NodeName:           m.config.Node.Name,
		DeploymentRestarts: m.restartCountsByDeployment(runtime),
		DeploymentMessages: m.restartMessagesByDeployment(runtime),
	}
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func resourceSpecEmpty(value ResourceSpec) bool {
	return value.CPUs == "" && value.Memory == "" && value.MemorySwap == "" && value.PIDsLimit == 0
}
