package runtimeagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (m *Manager) previousRuntimeState(key RuntimeKey) (Runtime, RuntimeStatus, bool) {
	m.mu.RLock()
	runtime, ok := m.specs[key.String()]
	status := m.statuses[key.String()]
	m.mu.RUnlock()
	if ok {
		return cloneRuntime(runtime), cloneRuntimeStatus(status), true
	}
	runtime, err := m.store.ReadRuntime(key.Namespace, key.Name)
	if err != nil {
		return Runtime{}, RuntimeStatus{}, false
	}
	status, _ = m.store.ReadStatus(key.Namespace, key.Name)
	return cloneRuntime(runtime), cloneRuntimeStatus(status), true
}

func (m *Manager) rollbackRuntime(ctx context.Context, failed Runtime, previous Runtime, previousStatus RuntimeStatus, cause error) error {
	key := KeyForRuntime(previous)
	message := fmt.Sprintf("Rolling update failed for generation %d; rolling back to generation %d: %v", failed.Metadata.Generation, previous.Metadata.Generation, cause)
	m.appendEvent(key.Namespace, key.Name, "Warning", "RollbackStarted", message)

	cleanupErr := m.removeOwnedContainersForGeneration(ctx, failed)
	restoreErr := m.restorePreviousRuntime(previous, previousStatus)
	if cleanupErr != nil || restoreErr != nil {
		rollbackErr := errors.Join(cleanupErr, restoreErr)
		m.appendEvent(key.Namespace, key.Name, "Warning", "RollbackFailed", rollbackErr.Error())
		return fmt.Errorf("rolling update failed and rollback failed: %w", errors.Join(cause, rollbackErr))
	}

	m.appendEvent(key.Namespace, key.Name, "Normal", "RollbackComplete", fmt.Sprintf("Rolled back to generation %d after failed update: %v", previous.Metadata.Generation, cause))
	logf(m.log, "AIFAR runtime rollback complete %s failedGeneration=%d restoredGeneration=%d\n", key.String(), failed.Metadata.Generation, previous.Metadata.Generation)
	return fmt.Errorf("rolling update failed and was rolled back: %w", cause)
}

func (m *Manager) restorePreviousRuntime(runtime Runtime, status RuntimeStatus) error {
	runtime = NormalizeRuntime(runtime)
	key := KeyForRuntime(runtime)
	if strings.TrimSpace(status.Phase) == "" {
		status = NewStatusWithOptions(runtime, RuntimePhaseRunning, m.runtimeEndpoints(runtime), nil, m.statusOptions(runtime))
	}
	m.mu.Lock()
	m.specs[key.String()] = cloneRuntime(runtime)
	m.statuses[key.String()] = cloneRuntimeStatus(status)
	m.mu.Unlock()
	if err := m.store.SaveRuntime(runtime); err != nil {
		return err
	}
	if err := m.store.SaveStatus(runtime, status); err != nil {
		return err
	}
	return nil
}

func (m *Manager) runtimeEndpoints(runtime Runtime) map[string][]Endpoint {
	key := KeyForRuntime(runtime)
	out := map[string][]Endpoint{}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, service := range runtime.Spec.Services {
		endpoints := m.endpoints[endpointKey(key, service.Name)]
		out[service.Name] = append([]Endpoint(nil), endpoints...)
	}
	return out
}

func (m *Manager) removeOwnedContainersForGeneration(ctx context.Context, runtime Runtime) error {
	key := KeyForRuntime(runtime)
	if runtime.Metadata.Generation <= 0 {
		return nil
	}
	result, err := m.docker(ctx,
		"ps", "-a",
		"--filter", "label=aifar.runtime/managed=true",
		"--filter", "label=aifar.runtime/namespace="+key.Namespace,
		"--filter", "label=aifar.runtime/name="+key.Name,
		"--filter", fmt.Sprintf("label=aifar.runtime/generation=%d", runtime.Metadata.Generation),
		"--format", "{{.Names}}",
	)
	if err != nil {
		return fmt.Errorf("list failed generation %d AIFAR pods for runtime %s: %w", runtime.Metadata.Generation, key.String(), err)
	}
	for _, name := range strings.Fields(result.Stdout) {
		if _, err := m.docker(ctx, "rm", "-f", name); err != nil {
			return fmt.Errorf("remove failed generation %d AIFAR pod %s: %w", runtime.Metadata.Generation, name, err)
		}
	}
	return nil
}
