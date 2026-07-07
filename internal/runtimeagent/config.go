package runtimeagent

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultAPIListen = "127.0.0.1:18081"

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value := strings.TrimSpace(node.Value)
	if value == "" {
		d.Duration = 0
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", value, err)
	}
	d.Duration = parsed
	return nil
}

type RuntimeConfig struct {
	API       APIConfig       `json:"api,omitempty" yaml:"api,omitempty"`
	State     StateConfig     `json:"state,omitempty" yaml:"state,omitempty"`
	Docker    DockerConfig    `json:"docker,omitempty" yaml:"docker,omitempty"`
	Container ContainerConfig `json:"container,omitempty" yaml:"container,omitempty"`
	Proxy     ProxyConfig     `json:"proxy,omitempty" yaml:"proxy,omitempty"`
	Reconcile ReconcileConfig `json:"reconcile,omitempty" yaml:"reconcile,omitempty"`
	Health    HealthConfig    `json:"health,omitempty" yaml:"health,omitempty"`
}

type APIConfig struct {
	Listen            string   `json:"listen,omitempty" yaml:"listen,omitempty"`
	ReadHeaderTimeout Duration `json:"readHeaderTimeout,omitempty" yaml:"readHeaderTimeout,omitempty"`
	ShutdownTimeout   Duration `json:"shutdownTimeout,omitempty" yaml:"shutdownTimeout,omitempty"`
}

type StateConfig struct {
	Dir string `json:"dir,omitempty" yaml:"dir,omitempty"`
}

type DockerConfig struct {
	Command       string   `json:"command,omitempty" yaml:"command,omitempty"`
	RestartPolicy string   `json:"restartPolicy,omitempty" yaml:"restartPolicy,omitempty"`
	AddHost       string   `json:"addHost,omitempty" yaml:"addHost,omitempty"`
	EventDebounce Duration `json:"eventDebounce,omitempty" yaml:"eventDebounce,omitempty"`
	EventBackoff  Duration `json:"eventBackoff,omitempty" yaml:"eventBackoff,omitempty"`
}

type ContainerConfig struct {
	ReadyTimeout            Duration `json:"readyTimeout,omitempty" yaml:"readyTimeout,omitempty"`
	ReadyPollInterval       Duration `json:"readyPollInterval,omitempty" yaml:"readyPollInterval,omitempty"`
	DiagnosticsLogTail      int      `json:"diagnosticsLogTail,omitempty" yaml:"diagnosticsLogTail,omitempty"`
	HTTPHealthCheckTemplate string   `json:"httpHealthCheckTemplate,omitempty" yaml:"httpHealthCheckTemplate,omitempty"`
}

type ProxyConfig struct {
	ReadHeaderTimeout Duration `json:"readHeaderTimeout,omitempty" yaml:"readHeaderTimeout,omitempty"`
}

type ReconcileConfig struct {
	Interval Duration `json:"interval,omitempty" yaml:"interval,omitempty"`
}

type HealthConfig struct {
	DockerTimeout Duration `json:"dockerTimeout,omitempty" yaml:"dockerTimeout,omitempty"`
}

func DefaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		API: APIConfig{
			Listen:            DefaultAPIListen,
			ReadHeaderTimeout: Duration{Duration: 10 * time.Second},
			ShutdownTimeout:   Duration{Duration: 30 * time.Second},
		},
		State: StateConfig{
			Dir: DefaultStateDir,
		},
		Docker: DockerConfig{
			Command:       "docker",
			RestartPolicy: "unless-stopped",
			AddHost:       "host.docker.internal:host-gateway",
			EventDebounce: Duration{Duration: 2 * time.Second},
			EventBackoff:  Duration{Duration: 5 * time.Second},
		},
		Container: ContainerConfig{
			ReadyTimeout:            Duration{Duration: 5 * time.Minute},
			ReadyPollInterval:       Duration{Duration: 3 * time.Second},
			DiagnosticsLogTail:      120,
			HTTPHealthCheckTemplate: "wget -qO- http://127.0.0.1:%d%s >/dev/null",
		},
		Proxy: ProxyConfig{
			ReadHeaderTimeout: Duration{Duration: 10 * time.Second},
		},
		Reconcile: ReconcileConfig{
			Interval: Duration{Duration: 30 * time.Second},
		},
		Health: HealthConfig{
			DockerTimeout: Duration{Duration: 5 * time.Second},
		},
	}
}

func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	config := DefaultRuntimeConfig()
	path = strings.TrimSpace(path)
	if path == "" {
		return config, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RuntimeConfig{}, err
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return RuntimeConfig{}, err
	}
	config = NormalizeRuntimeConfig(config)
	if err := ValidateRuntimeConfig(config); err != nil {
		return RuntimeConfig{}, err
	}
	return config, nil
}

func NormalizeRuntimeConfig(config RuntimeConfig) RuntimeConfig {
	defaults := DefaultRuntimeConfig()
	config.API.Listen = defaultString(config.API.Listen, defaults.API.Listen)
	config.API.ReadHeaderTimeout = defaultDuration(config.API.ReadHeaderTimeout, defaults.API.ReadHeaderTimeout)
	config.API.ShutdownTimeout = defaultDuration(config.API.ShutdownTimeout, defaults.API.ShutdownTimeout)
	config.State.Dir = defaultString(config.State.Dir, defaults.State.Dir)
	config.Docker.Command = defaultString(config.Docker.Command, defaults.Docker.Command)
	config.Docker.RestartPolicy = defaultString(config.Docker.RestartPolicy, defaults.Docker.RestartPolicy)
	config.Docker.AddHost = strings.TrimSpace(config.Docker.AddHost)
	if config.Docker.AddHost == "" {
		config.Docker.AddHost = defaults.Docker.AddHost
	}
	config.Docker.EventDebounce = defaultDuration(config.Docker.EventDebounce, defaults.Docker.EventDebounce)
	config.Docker.EventBackoff = defaultDuration(config.Docker.EventBackoff, defaults.Docker.EventBackoff)
	config.Container.ReadyTimeout = defaultDuration(config.Container.ReadyTimeout, defaults.Container.ReadyTimeout)
	config.Container.ReadyPollInterval = defaultDuration(config.Container.ReadyPollInterval, defaults.Container.ReadyPollInterval)
	if config.Container.DiagnosticsLogTail <= 0 {
		config.Container.DiagnosticsLogTail = defaults.Container.DiagnosticsLogTail
	}
	config.Container.HTTPHealthCheckTemplate = defaultString(config.Container.HTTPHealthCheckTemplate, defaults.Container.HTTPHealthCheckTemplate)
	config.Proxy.ReadHeaderTimeout = defaultDuration(config.Proxy.ReadHeaderTimeout, defaults.Proxy.ReadHeaderTimeout)
	config.Reconcile.Interval = defaultDuration(config.Reconcile.Interval, defaults.Reconcile.Interval)
	config.Health.DockerTimeout = defaultDuration(config.Health.DockerTimeout, defaults.Health.DockerTimeout)
	return config
}

func ValidateRuntimeConfig(config RuntimeConfig) error {
	if err := validateListenAddress("api.listen", config.API.Listen); err != nil {
		return err
	}
	if strings.TrimSpace(config.State.Dir) == "" {
		return errorsForField("state.dir", "must not be empty")
	}
	if strings.TrimSpace(config.Docker.Command) == "" {
		return errorsForField("docker.command", "must not be empty")
	}
	if config.API.ReadHeaderTimeout.Duration <= 0 {
		return errorsForField("api.readHeaderTimeout", "must be greater than zero")
	}
	if config.API.ShutdownTimeout.Duration <= 0 {
		return errorsForField("api.shutdownTimeout", "must be greater than zero")
	}
	if config.Docker.EventDebounce.Duration <= 0 {
		return errorsForField("docker.eventDebounce", "must be greater than zero")
	}
	if config.Docker.EventBackoff.Duration <= 0 {
		return errorsForField("docker.eventBackoff", "must be greater than zero")
	}
	if config.Container.ReadyTimeout.Duration <= 0 {
		return errorsForField("container.readyTimeout", "must be greater than zero")
	}
	if config.Container.ReadyPollInterval.Duration <= 0 {
		return errorsForField("container.readyPollInterval", "must be greater than zero")
	}
	if config.Container.DiagnosticsLogTail <= 0 {
		return errorsForField("container.diagnosticsLogTail", "must be greater than zero")
	}
	if !strings.Contains(config.Container.HTTPHealthCheckTemplate, "%d") || !strings.Contains(config.Container.HTTPHealthCheckTemplate, "%s") {
		return errorsForField("container.httpHealthCheckTemplate", "must contain %d for port and %s for path")
	}
	if config.Proxy.ReadHeaderTimeout.Duration <= 0 {
		return errorsForField("proxy.readHeaderTimeout", "must be greater than zero")
	}
	if config.Reconcile.Interval.Duration <= 0 {
		return errorsForField("reconcile.interval", "must be greater than zero")
	}
	if config.Health.DockerTimeout.Duration <= 0 {
		return errorsForField("health.dockerTimeout", "must be greater than zero")
	}
	return nil
}

func validateListenAddress(field, address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errorsForField(field, "must be a host:port address")
	}
	if strings.TrimSpace(port) == "" {
		return errorsForField(field, "must include a port")
	}
	if strings.Contains(host, "/") {
		return errorsForField(field, "must be a TCP host:port address")
	}
	return nil
}

func errorsForField(field, message string) error {
	return fmt.Errorf("invalid runtime config %s: %s", field, message)
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func defaultDuration(value, fallback Duration) Duration {
	if value.Duration <= 0 {
		return fallback
	}
	return value
}
