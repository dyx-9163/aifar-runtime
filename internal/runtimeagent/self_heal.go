package runtimeagent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type restartRecord struct {
	Count         int
	FailureStreak int
	LastAttempt   time.Time
	NextAttempt   time.Time
	LastReason    string
}

type containerRuntimeState struct {
	Running  bool
	Status   string
	ExitCode int
	Health   string
}

func (s containerRuntimeState) Ready() bool {
	return s.Running && (s.Health == "" || s.Health == "healthy")
}

func (s containerRuntimeState) NeedsRestart() bool {
	if !s.Running {
		return true
	}
	return s.Health == "unhealthy"
}

func (s containerRuntimeState) Reason() string {
	if !s.Running {
		status := strings.TrimSpace(s.Status)
		if status == "" {
			status = "not-running"
		}
		return fmt.Sprintf("container status=%s exitCode=%d", status, s.ExitCode)
	}
	if s.Health == "unhealthy" {
		return "container health=unhealthy"
	}
	return "container is not ready"
}

func (m *Manager) containerRuntimeState(ctx context.Context, name string) (containerRuntimeState, error) {
	result, err := m.docker(ctx, "inspect", "-f", `{{.State.Running}}|{{.State.Status}}|{{.State.ExitCode}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}`, name)
	if err != nil {
		return containerRuntimeState{}, err
	}
	parts := strings.SplitN(strings.TrimSpace(result.Stdout), "|", 4)
	if len(parts) != 4 {
		return containerRuntimeState{}, fmt.Errorf("inspect container %s returned unexpected state %q", name, strings.TrimSpace(result.Stdout))
	}
	exitCode, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
	return containerRuntimeState{
		Running:  strings.TrimSpace(parts[0]) == "true",
		Status:   strings.TrimSpace(parts[1]),
		ExitCode: exitCode,
		Health:   strings.TrimSpace(parts[3]),
	}, nil
}

func (m *Manager) healContainer(ctx context.Context, runtime Runtime, deployment DeploymentSpec, replica int, name string, state containerRuntimeState) (bool, error) {
	key := KeyForRuntime(runtime)
	reason := state.Reason()
	if m.config.SelfHeal.Enabled == nil || !*m.config.SelfHeal.Enabled {
		m.setRestartMessage(name, reason)
		m.appendEvent(key.Namespace, key.Name, "Warning", "ContainerUnhealthy", fmt.Sprintf("%s is unhealthy but self-heal is disabled: %s", name, reason))
		return false, nil
	}
	now := time.Now()
	record := m.restartRecord(name)
	if record.FailureStreak >= m.config.SelfHeal.MaxRestarts {
		message := fmt.Sprintf("%s restart limit exceeded after %d consecutive attempts: %s", name, record.FailureStreak, reason)
		m.setRestartMessage(name, message)
		m.appendEvent(key.Namespace, key.Name, "Warning", "RestartLimitExceeded", message)
		return false, nil
	}
	if now.Before(record.NextAttempt) {
		m.setRestartMessage(name, fmt.Sprintf("%s waiting for restart backoff until %s: %s", name, record.NextAttempt.UTC().Format(time.RFC3339), reason))
		return false, nil
	}
	record.Count++
	record.FailureStreak++
	record.LastAttempt = now
	record.NextAttempt = now.Add(time.Duration(record.FailureStreak) * m.config.SelfHeal.Backoff.Duration)
	record.LastReason = reason
	m.setRestartRecord(name, record)
	m.appendEvent(key.Namespace, key.Name, "Warning", "ContainerRestarting", fmt.Sprintf("Restarting %s attempt=%d reason=%s", name, record.Count, reason))
	m.updateRuntimeStatus(runtime, RuntimePhaseUpdating, nil, nil)
	if _, err := m.docker(ctx, "rm", "-f", name); err != nil {
		return true, fmt.Errorf("self-heal remove unhealthy AIFAR pod %s: %w", name, err)
	}
	if err := m.runContainer(ctx, runtime, deployment, replica, name); err != nil {
		return true, fmt.Errorf("self-heal restart AIFAR pod %s: %w", name, err)
	}
	return true, nil
}

func (m *Manager) restartRecord(name string) restartRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.restarts[name]
}

func (m *Manager) setRestartRecord(name string, record restartRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restarts[name] = record
}

func (m *Manager) markRestartHealthy(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.restarts[name]
	if record.Count == 0 && record.FailureStreak == 0 {
		return
	}
	record.FailureStreak = 0
	record.LastReason = ""
	m.restarts[name] = record
}

func (m *Manager) setRestartMessage(name, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.restarts[name]
	record.LastReason = strings.TrimSpace(message)
	m.restarts[name] = record
}

func (m *Manager) restartCountsByDeployment(runtime Runtime) map[string]int {
	out := map[string]int{}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, deployment := range runtime.Spec.Deployments {
		for replica := 1; replica <= deploymentReplicas(deployment); replica++ {
			name := containerNameForDeployment(runtime, deployment, replica)
			out[deployment.Name] += m.restarts[name].Count
		}
	}
	return out
}

func (m *Manager) restartMessagesByDeployment(runtime Runtime) map[string]string {
	out := map[string]string{}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, deployment := range runtime.Spec.Deployments {
		for replica := 1; replica <= deploymentReplicas(deployment); replica++ {
			name := containerNameForDeployment(runtime, deployment, replica)
			record := m.restarts[name]
			if strings.TrimSpace(record.LastReason) != "" {
				out[deployment.Name] = record.LastReason
			}
		}
	}
	return out
}
