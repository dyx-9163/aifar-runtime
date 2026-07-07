package runtimeagent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (m *Manager) ensureNetwork(ctx context.Context, network string) error {
	network = strings.TrimSpace(network)
	if network == "" {
		return errors.New("docker network is required")
	}
	if _, err := m.docker(ctx, "network", "inspect", network); err == nil {
		return nil
	}
	if _, err := m.docker(ctx, "network", "create", network); err != nil {
		return fmt.Errorf("ensure docker network %s: %w", network, err)
	}
	return nil
}

func (m *Manager) reconcileDeployments(ctx context.Context, runtime Runtime) error {
	for _, deployment := range runtime.Spec.Deployments {
		if err := m.ensureDeployment(ctx, runtime, deployment); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ensureDeployment(ctx context.Context, runtime Runtime, deployment DeploymentSpec) error {
	replicas := deploymentReplicas(deployment)
	for replica := 1; replica <= replicas; replica++ {
		name := containerNameForDeployment(runtime, deployment, replica)
		exists, err := m.containerExists(ctx, name)
		if err != nil {
			return err
		}
		if exists {
			recreate, err := m.containerNeedsRecreate(ctx, name, deployment)
			if err != nil {
				return err
			}
			if recreate {
				if deployment.Strategy.Type == "rollingupdate" && deployment.Strategy.RollingUpdate != nil && deployment.Strategy.RollingUpdate.MaxSurge > 0 {
					if err := m.replaceContainerWithSurge(ctx, runtime, deployment, replica, name); err != nil {
						return err
					}
					continue
				}
				if _, err := m.docker(ctx, "rm", "-f", name); err != nil {
					return fmt.Errorf("replace drifted AIFAR pod %s: %w", name, err)
				}
				exists = false
			}
		}
		if exists {
			state, err := m.containerRuntimeState(ctx, name)
			if err != nil {
				return err
			}
			if state.Ready() {
				m.markRestartHealthy(name)
			}
			if state.NeedsRestart() {
				restarted, err := m.healContainer(ctx, runtime, deployment, replica, name, state)
				if err != nil {
					return err
				}
				if restarted {
					continue
				}
			}
		}
		if !exists {
			if err := m.runContainer(ctx, runtime, deployment, replica, name); err != nil {
				return err
			}
		}
	}
	return m.removeExtraReplicas(ctx, runtime, deployment)
}

func (m *Manager) replaceContainerWithSurge(ctx context.Context, runtime Runtime, deployment DeploymentSpec, replica int, canonicalName string) error {
	surgeName := sanitizeDockerName(fmt.Sprintf("%s-surge-g%d", canonicalName, runtime.Metadata.Generation))
	if err := m.runContainer(ctx, runtime, deployment, replica, surgeName); err != nil {
		return err
	}
	if _, err := m.docker(ctx, "rm", "-f", canonicalName); err != nil {
		return fmt.Errorf("replace drifted AIFAR pod %s: %w", canonicalName, err)
	}
	if _, err := m.docker(ctx, "rename", surgeName, canonicalName); err != nil {
		return fmt.Errorf("rename surge AIFAR pod %s to %s: %w", surgeName, canonicalName, err)
	}
	return nil
}

func (m *Manager) removeExtraReplicas(ctx context.Context, runtime Runtime, deployment DeploymentSpec) error {
	key := KeyForRuntime(runtime)
	result, err := m.docker(ctx,
		"ps", "-a",
		"--filter", "label=aifar.runtime/managed=true",
		"--filter", "label=aifar.runtime/namespace="+key.Namespace,
		"--filter", "label=aifar.runtime/name="+key.Name,
		"--filter", "label=aifar.runtime/deployment="+deployment.Name,
		"--format", `{{.Names}}|{{.Label "aifar.runtime/replica"}}|{{.Label "aifar.runtime/revision"}}`,
	)
	if err != nil {
		return fmt.Errorf("list extra AIFAR pods for deployment %s: %w", deployment.Name, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		replica, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		revision := ""
		if len(parts) == 3 {
			revision = strings.TrimSpace(parts[2])
		}
		if replica > deploymentReplicas(deployment) || (revision != "" && revision != deployment.Revision) {
			if _, err := m.docker(ctx, "rm", "-f", name); err != nil {
				return fmt.Errorf("remove extra AIFAR pod %s: %w", name, err)
			}
			logf(m.log, "AIFAR runtime pod removed deployment=%s replica=%d container=%s\n", deployment.Name, replica, name)
		}
	}
	return nil
}
