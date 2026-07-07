package runtimeagent

type RuntimeMetrics struct {
	RuntimeVersion       string
	RuntimeCount         int
	ListenerCount        int
	DesiredReplicas      int
	ReadyReplicas        int
	FailedRuntimeCount   int
	DegradedRuntimeCount int
	ContainerRestarts    int
	EndpointCount        int
}

func (m *Manager) Metrics() RuntimeMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	metrics := RuntimeMetrics{
		RuntimeVersion: RuntimeVersion,
		RuntimeCount:   len(m.specs),
		ListenerCount:  len(m.servers),
	}
	for key, runtime := range m.specs {
		for _, deployment := range runtime.Spec.Deployments {
			metrics.DesiredReplicas += deploymentReplicas(deployment)
		}
		status := m.statuses[key]
		if status.Phase == RuntimePhaseFailed {
			metrics.FailedRuntimeCount++
		}
		if status.Phase == RuntimePhaseDegraded {
			metrics.DegradedRuntimeCount++
		}
		for _, deployment := range status.Deployments {
			metrics.ReadyReplicas += deployment.Ready
			metrics.ContainerRestarts += deployment.Restarts
		}
	}
	for _, endpoints := range m.endpoints {
		metrics.EndpointCount += len(endpoints)
	}
	return metrics
}
