package runtimeagent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func (m *Manager) containerExists(ctx context.Context, name string) (bool, error) {
	_, err := m.docker(ctx, "inspect", "-f", "{{.Id}}", name)
	if err == nil {
		return true, nil
	}
	return false, nil
}

func (m *Manager) containerNeedsRecreate(ctx context.Context, name string, deployment DeploymentSpec) (bool, error) {
	result, err := m.docker(ctx, "inspect", "-f", `{{index .Config.Labels "aifar.runtime/spec-hash"}}`, name)
	if err != nil || strings.TrimSpace(result.Stdout) == "" {
		result, err = m.docker(ctx, "inspect", "-f", `{{index .Config.Labels "aifar.spec-hash"}}`, name)
	}
	if err != nil {
		return false, nil
	}
	current := strings.TrimSpace(result.Stdout)
	return current == "" || current != deploymentSpecHash(deployment), nil
}

func (m *Manager) runContainer(ctx context.Context, runtime Runtime, deployment DeploymentSpec, replica int, name string) error {
	m.updateRuntimeStatus(runtime, RuntimePhasePulling, nil, nil)
	if err := m.pullImageWithSecrets(ctx, runtime, deployment); err != nil {
		return err
	}
	m.updateRuntimeStatus(runtime, RuntimePhaseStarting, nil, nil)
	key := KeyForRuntime(runtime)
	args := []string{"run", "-d", "--name", name, "--restart", m.config.Docker.RestartPolicy}
	args = append(args,
		"--label", "aifar.runtime/managed=true",
		"--label", "aifar.runtime/namespace="+key.Namespace,
		"--label", "aifar.runtime/name="+key.Name,
		"--label", "aifar.runtime/deployment="+deployment.Name,
		"--label", fmt.Sprintf("aifar.runtime/replica=%d", replica),
		"--label", fmt.Sprintf("aifar.runtime/generation=%d", runtime.Metadata.Generation),
		"--label", "aifar.runtime/revision="+deployment.Revision,
		"--label", "aifar.runtime/spec-hash="+deploymentSpecHash(deployment),
		"--label", "aifar.app=aifar",
		"--label", "aifar.component=pod",
		"--label", "aifar.instance="+key.Name,
		"--label", "aifar.service="+deployment.Name,
		"--label", fmt.Sprintf("aifar.replica=%d", replica),
		"--label", "aifar.revision="+deployment.Revision,
		"--network", runtime.Spec.Network,
	)
	if m.config.Docker.AddHost != "" {
		args = append(args, "--add-host", m.config.Docker.AddHost)
	}
	for key, value := range deployment.Labels {
		if strings.TrimSpace(key) != "" {
			args = append(args, "--label", key+"="+value)
		}
	}
	if deployment.Resources.CPUs != "" {
		args = append(args, "--cpus", deployment.Resources.CPUs)
	}
	if deployment.Resources.Memory != "" {
		memorySwap := deployment.Resources.Memory
		if deployment.Resources.MemorySwap != "" {
			memorySwap = deployment.Resources.MemorySwap
		}
		args = append(args, "--memory", deployment.Resources.Memory, "--memory-swap", memorySwap)
	}
	if deployment.Resources.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(deployment.Resources.PIDsLimit))
	}
	if healthCommand := m.healthCheckCommand(deployment); healthCommand != "" {
		args = append(args, "--health-cmd", healthCommand)
		if deployment.HealthCheck.Interval != "" {
			args = append(args, "--health-interval", deployment.HealthCheck.Interval)
		}
		if deployment.HealthCheck.Timeout != "" {
			args = append(args, "--health-timeout", deployment.HealthCheck.Timeout)
		}
		if deployment.HealthCheck.Retries > 0 {
			args = append(args, "--health-retries", strconv.Itoa(deployment.HealthCheck.Retries))
		}
		if deployment.HealthCheck.StartPeriod != "" {
			args = append(args, "--health-start-period", deployment.HealthCheck.StartPeriod)
		}
	}
	for _, envFile := range deployment.EnvFiles {
		if strings.TrimSpace(envFile) != "" {
			args = append(args, "--env-file", envFile)
		}
	}
	for _, source := range deployment.EnvFrom {
		if strings.EqualFold(source.Type, "file") && strings.TrimSpace(source.Path) != "" {
			args = append(args, "--env-file", strings.TrimSpace(source.Path))
		}
		if strings.EqualFold(source.Type, "secret") {
			secret, ok := secretByName(runtime, source.Name)
			if !ok {
				return fmt.Errorf("deployment %s envFrom secret %s is not defined", deployment.Name, source.Name)
			}
			values, err := secretValues(secret)
			if err != nil {
				return err
			}
			for key, value := range values {
				args = append(args, "-e", key+"="+value)
			}
		}
	}
	for key, value := range deployment.Env {
		if strings.TrimSpace(key) != "" {
			value = strings.ReplaceAll(value, "${containerName}", name)
			args = append(args, "-e", key+"="+value)
		}
	}
	for _, volume := range deployment.Volumes {
		if volume.Source == "" || volume.Target == "" {
			continue
		}
		mount := volume.Source + ":" + volume.Target
		if volume.ReadOnly {
			mount += ":ro"
		}
		args = append(args, "-v", mount)
	}
	if len(deployment.Entrypoint) > 0 {
		entrypoint := strings.TrimSpace(deployment.Entrypoint[0])
		if entrypoint != "" {
			args = append(args, "--entrypoint", entrypoint)
		}
	}
	args = append(args, deployment.Image)
	if len(deployment.Entrypoint) > 1 {
		for _, arg := range deployment.Entrypoint[1:] {
			if strings.TrimSpace(arg) != "" {
				args = append(args, arg)
			}
		}
	}
	args = append(args, deployment.Command...)
	if _, err := m.docker(ctx, args...); err != nil {
		return fmt.Errorf("start AIFAR pod %s: %w", name, err)
	}
	if err := m.waitContainerReady(ctx, name); err != nil {
		return err
	}
	logf(m.log, "AIFAR runtime pod started runtime=%s deployment=%s replica=%d container=%s\n", KeyForRuntime(runtime).String(), deployment.Name, replica, name)
	return nil
}

func (m *Manager) pullImageWithSecrets(ctx context.Context, runtime Runtime, deployment DeploymentSpec) error {
	if len(deployment.ImagePullSecrets) == 0 {
		return nil
	}
	for _, ref := range deployment.ImagePullSecrets {
		secret, ok := secretByName(runtime, ref.Name)
		if !ok {
			return fmt.Errorf("deployment %s imagePullSecret %s is not defined", deployment.Name, ref.Name)
		}
		if err := m.pullImageWithSecret(ctx, deployment.Image, secret); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) pullImageWithSecret(ctx context.Context, image string, secret SecretSpec) error {
	data, err := secretValues(secret)
	if err != nil {
		return err
	}
	if secret.Type == "dockerconfigjson" {
		configPath := strings.TrimSpace(data["configPath"])
		if configPath == "" {
			return fmt.Errorf("imagePullSecret %s dockerconfigjson requires stringData.configPath", secret.Name)
		}
		if _, err := m.docker(ctx, "--config", configPath, "pull", image); err != nil {
			return fmt.Errorf("pull image %s with docker config secret %s: %w", image, secret.Name, err)
		}
		return nil
	}
	if secret.Type != "registry-auth" {
		return fmt.Errorf("imagePullSecret %s type %s is not supported", secret.Name, secret.Type)
	}
	registry := strings.TrimSpace(data["server"])
	if registry == "" {
		registry = registryForImage(image)
	}
	username := strings.TrimSpace(data["username"])
	password := data["password"]
	if registry == "" || username == "" || password == "" {
		return fmt.Errorf("imagePullSecret %s requires stringData.server, username, and password", secret.Name)
	}
	configDir, err := os.MkdirTemp("", "aifar-docker-config-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(configDir)
	if _, err := m.dockerWithInput(ctx, password+"\n", "--config", configDir, "login", registry, "--username", username, "--password-stdin"); err != nil {
		return fmt.Errorf("docker login registry %s for secret %s: %w", registry, secret.Name, err)
	}
	if _, err := m.docker(ctx, "--config", configDir, "pull", image); err != nil {
		return fmt.Errorf("pull image %s with registry secret %s: %w", image, secret.Name, err)
	}
	return nil
}

func (m *Manager) dockerWithInput(ctx context.Context, input string, args ...string) (CommandResult, error) {
	if runner, ok := m.runner.(InputCommandRunner); ok {
		return runner.RunWithInput(ctx, input, m.config.Docker.Command, args...)
	}
	return CommandResult{}, errors.New("configured command runner does not support stdin")
}

func secretValues(secret SecretSpec) (map[string]string, error) {
	out := map[string]string{}
	for key, value := range secret.Data {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("secret %s data[%s] must be base64 encoded", secret.Name, key)
		}
		out[strings.TrimSpace(key)] = string(decoded)
	}
	for key, value := range secret.StringData {
		out[strings.TrimSpace(key)] = value
	}
	delete(out, "")
	return out, nil
}

func registryForImage(image string) string {
	first, _, found := strings.Cut(strings.TrimSpace(image), "/")
	if !found {
		return "index.docker.io"
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return "index.docker.io"
}

func (m *Manager) waitContainerReady(ctx context.Context, name string) error {
	deadline := time.Now().Add(m.config.Container.ReadyTimeout.Duration)
	lastInspect := ""
	for {
		result, err := m.docker(ctx, "inspect", "-f", `{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}`, name)
		if err == nil {
			lastInspect = strings.TrimSpace(result.Stdout)
			parts := strings.SplitN(strings.TrimSpace(result.Stdout), "|", 2)
			running := len(parts) > 0 && parts[0] == "true"
			health := ""
			if len(parts) > 1 {
				health = parts[1]
			}
			if running && (health == "" || health == "healthy") {
				return nil
			}
		} else {
			lastInspect = strings.TrimSpace(err.Error())
			if strings.TrimSpace(result.Stderr) != "" {
				lastInspect += ": " + strings.TrimSpace(result.Stderr)
			}
		}
		if time.Now().After(deadline) {
			diagnostics := m.containerReadyDiagnostics(ctx, name, lastInspect)
			if diagnostics != "" {
				return fmt.Errorf("AIFAR pod did not become ready: %s\n%s", name, diagnostics)
			}
			return fmt.Errorf("AIFAR pod did not become ready: %s", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.config.Container.ReadyPollInterval.Duration):
		}
	}
}

func (m *Manager) containerReadyDiagnostics(ctx context.Context, name, lastInspect string) string {
	var b strings.Builder
	if strings.TrimSpace(lastInspect) != "" {
		fmt.Fprintf(&b, "last inspect: %s\n", trimDiagnosticOutput(lastInspect, 1024))
	}
	inspectFormat := `status={{.State.Status}} running={{.State.Running}} exitCode={{.State.ExitCode}} error={{.State.Error}} oomKilled={{.State.OOMKilled}}{{if .State.Health}} health={{.State.Health.Status}}{{end}}`
	if result, err := m.docker(ctx, "inspect", "-f", inspectFormat, name); err != nil {
		fmt.Fprintf(&b, "inspect failed: %v", err)
		if strings.TrimSpace(result.Stderr) != "" {
			fmt.Fprintf(&b, ": %s", trimDiagnosticOutput(strings.TrimSpace(result.Stderr), 1024))
		}
		b.WriteString("\n")
	} else if strings.TrimSpace(result.Stdout) != "" {
		fmt.Fprintf(&b, "inspect: %s\n", trimDiagnosticOutput(strings.TrimSpace(result.Stdout), 2048))
	}
	healthFormat := `{{if .State.Health}}{{range .State.Health.Log}}{{println .Start "exit=" .ExitCode "output=" .Output}}{{end}}{{end}}`
	if result, err := m.docker(ctx, "inspect", "-f", healthFormat, name); err == nil && strings.TrimSpace(result.Stdout) != "" {
		fmt.Fprintf(&b, "health log:\n%s\n", trimDiagnosticOutput(strings.TrimSpace(result.Stdout), 4096))
	}
	if result, err := m.docker(ctx, "logs", "--tail", strconv.Itoa(m.config.Container.DiagnosticsLogTail), name); err != nil {
		fmt.Fprintf(&b, "logs failed: %v", err)
		if strings.TrimSpace(result.Stderr) != "" {
			fmt.Fprintf(&b, ": %s", trimDiagnosticOutput(strings.TrimSpace(result.Stderr), 1024))
		}
		b.WriteString("\n")
	} else {
		logs := strings.TrimSpace(strings.TrimSpace(result.Stdout) + "\n" + strings.TrimSpace(result.Stderr))
		if logs != "" {
			fmt.Fprintf(&b, "logs:\n%s\n", trimDiagnosticOutput(logs, 8192))
		}
	}
	return strings.TrimSpace(b.String())
}

func trimDiagnosticOutput(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "...(truncated)"
}

func containerNameForDeployment(runtime Runtime, deployment DeploymentSpec, replica int) string {
	key := KeyForRuntime(runtime)
	revision := strings.TrimSpace(deployment.Revision)
	if revision == "" {
		revision = "current"
	}
	return sanitizeDockerName(fmt.Sprintf("aifar-pod-%s-%s-%s-%s-r%d", key.Namespace, key.Name, deployment.Name, revision, replica))
}

func sanitizeDockerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "aifar-pod"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "aifar-pod"
	}
	return out
}

func deploymentSpecHash(deployment DeploymentSpec) string {
	type hashDeployment struct {
		Name             string                 `json:"name"`
		Image            string                 `json:"image,omitempty"`
		ImagePullSecrets []LocalObjectReference `json:"imagePullSecrets,omitempty"`
		Strategy         DeploymentStrategy     `json:"strategy,omitempty"`
		Selector         map[string]string      `json:"selector,omitempty"`
		Ports            []ContainerPort        `json:"ports,omitempty"`
		Env              map[string]string      `json:"env,omitempty"`
		EnvFiles         []string               `json:"envFiles,omitempty"`
		EnvFrom          []EnvFromSource        `json:"envFrom,omitempty"`
		Volumes          []VolumeMount          `json:"volumes,omitempty"`
		Resources        ResourceSpec           `json:"resources,omitempty"`
		HealthCheck      HealthCheckSpec        `json:"healthCheck,omitempty"`
		Entrypoint       []string               `json:"entrypoint,omitempty"`
		Command          []string               `json:"command,omitempty"`
		Labels           map[string]string      `json:"labels,omitempty"`
		Revision         string                 `json:"revision,omitempty"`
	}
	data, _ := json.Marshal(hashDeployment{
		Name:             deployment.Name,
		Image:            deployment.Image,
		ImagePullSecrets: deployment.ImagePullSecrets,
		Strategy:         deployment.Strategy,
		Selector:         deployment.Selector,
		Ports:            deployment.Ports,
		Env:              deployment.Env,
		EnvFiles:         deployment.EnvFiles,
		EnvFrom:          deployment.EnvFrom,
		Volumes:          deployment.Volumes,
		Resources:        deployment.Resources,
		HealthCheck:      deployment.HealthCheck,
		Entrypoint:       deployment.Entrypoint,
		Command:          deployment.Command,
		Labels:           deployment.Labels,
		Revision:         deployment.Revision,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) healthCheckCommand(deployment DeploymentSpec) string {
	if strings.TrimSpace(deployment.HealthCheck.Command) != "" {
		return deployment.HealthCheck.Command
	}
	if deployment.HealthCheck.HTTPGet == nil {
		return ""
	}
	port := 0
	if deployment.HealthCheck.HTTPGet.Port.IntVal > 0 {
		port = deployment.HealthCheck.HTTPGet.Port.IntVal
	} else {
		for _, candidate := range deployment.Ports {
			if candidate.Name == deployment.HealthCheck.HTTPGet.Port.StrVal {
				port = candidate.ContainerPort
				break
			}
		}
	}
	if port <= 0 {
		return ""
	}
	path := cleanIngressPath(deployment.HealthCheck.HTTPGet.Path)
	return fmt.Sprintf(m.config.Container.HTTPHealthCheckTemplate, port, path)
}

func (m *Manager) removeOwnedContainers(ctx context.Context, runtime Runtime) error {
	key := KeyForRuntime(runtime)
	result, err := m.docker(ctx,
		"ps", "-a",
		"--filter", "label=aifar.runtime/managed=true",
		"--filter", "label=aifar.runtime/namespace="+key.Namespace,
		"--filter", "label=aifar.runtime/name="+key.Name,
		"--format", "{{.Names}}",
	)
	if err != nil {
		return fmt.Errorf("list owned AIFAR pods for runtime %s: %w", key.String(), err)
	}
	for _, name := range strings.Fields(result.Stdout) {
		if _, err := m.docker(ctx, "rm", "-f", name); err != nil {
			return fmt.Errorf("remove owned container %s: %w", name, err)
		}
	}
	return nil
}
