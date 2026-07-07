package runtimeagent

import (
	"context"
	"time"
)

func (m *Manager) Resync(ctx context.Context) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	for _, runtime := range m.snapshotRuntimes() {
		if err := m.reconcileDeployments(ctx, runtime); err != nil {
			return err
		}
		refreshed, err := m.refreshRuntimeEndpoints(ctx, runtime)
		if err != nil {
			return err
		}
		key := KeyForRuntime(runtime)
		m.mu.Lock()
		for service, endpoints := range refreshed {
			m.endpoints[endpointKey(key, service)] = endpoints
		}
		m.statuses[key.String()] = NewStatus(runtime, "Ready", refreshed, nil)
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) StartRuntimeResync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Resync(ctx); err != nil {
				logf(m.log, "AIFAR runtime periodic resync failed: %v\n", err)
			}
		}
	}
}
