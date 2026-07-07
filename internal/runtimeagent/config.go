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
	API           APIConfig           `json:"api,omitempty" yaml:"api,omitempty"`
	Node          NodeConfig          `json:"node,omitempty" yaml:"node,omitempty"`
	State         StateConfig         `json:"state,omitempty" yaml:"state,omitempty"`
	Docker        DockerConfig        `json:"docker,omitempty" yaml:"docker,omitempty"`
	Container     ContainerConfig     `json:"container,omitempty" yaml:"container,omitempty"`
	SelfHeal      SelfHealConfig      `json:"selfHeal,omitempty" yaml:"selfHeal,omitempty"`
	Proxy         ProxyConfig         `json:"proxy,omitempty" yaml:"proxy,omitempty"`
	Reconcile     ReconcileConfig     `json:"reconcile,omitempty" yaml:"reconcile,omitempty"`
	Health        HealthConfig        `json:"health,omitempty" yaml:"health,omitempty"`
	Security      SecurityConfig      `json:"security,omitempty" yaml:"security,omitempty"`
	Observability ObservabilityConfig `json:"observability,omitempty" yaml:"observability,omitempty"`
	Log           LogConfig           `json:"log,omitempty" yaml:"log,omitempty"`
}

type APIConfig struct {
	Listen            string   `json:"listen,omitempty" yaml:"listen,omitempty"`
	ReadHeaderTimeout Duration `json:"readHeaderTimeout,omitempty" yaml:"readHeaderTimeout,omitempty"`
	ShutdownTimeout   Duration `json:"shutdownTimeout,omitempty" yaml:"shutdownTimeout,omitempty"`
}

type NodeConfig struct {
	Name        string            `json:"name,omitempty" yaml:"name,omitempty"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Capacity    ResourceSpec      `json:"capacity,omitempty" yaml:"capacity,omitempty"`
	Allocatable ResourceSpec      `json:"allocatable,omitempty" yaml:"allocatable,omitempty"`
}

type StateConfig struct {
	Backend string     `json:"backend,omitempty" yaml:"backend,omitempty"`
	Dir     string     `json:"dir,omitempty" yaml:"dir,omitempty"`
	Etcd    EtcdConfig `json:"etcd,omitempty" yaml:"etcd,omitempty"`
}

type EtcdConfig struct {
	Endpoints   []string `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
	Prefix      string   `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	DialTimeout Duration `json:"dialTimeout,omitempty" yaml:"dialTimeout,omitempty"`
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

type SelfHealConfig struct {
	Enabled     *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	MaxRestarts int      `json:"maxRestarts,omitempty" yaml:"maxRestarts,omitempty"`
	Backoff     Duration `json:"backoff,omitempty" yaml:"backoff,omitempty"`
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

type SecurityConfig struct {
	BearerToken     string     `json:"bearerToken,omitempty" yaml:"bearerToken,omitempty"`
	BearerTokenFile string     `json:"bearerTokenFile,omitempty" yaml:"bearerTokenFile,omitempty"`
	TLSCertFile     string     `json:"tlsCertFile,omitempty" yaml:"tlsCertFile,omitempty"`
	TLSKeyFile      string     `json:"tlsKeyFile,omitempty" yaml:"tlsKeyFile,omitempty"`
	RBAC            RBACConfig `json:"rbac,omitempty" yaml:"rbac,omitempty"`
}

type RBACConfig struct {
	Enabled bool                `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Tokens  []AccessTokenConfig `json:"tokens,omitempty" yaml:"tokens,omitempty"`
}

type AccessTokenConfig struct {
	Name      string `json:"name,omitempty" yaml:"name,omitempty"`
	Role      string `json:"role,omitempty" yaml:"role,omitempty"`
	Token     string `json:"token,omitempty" yaml:"token,omitempty"`
	TokenFile string `json:"tokenFile,omitempty" yaml:"tokenFile,omitempty"`
}

type ObservabilityConfig struct {
	MetricsEnabled bool `json:"metricsEnabled,omitempty" yaml:"metricsEnabled,omitempty"`
}

type LogConfig struct {
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
	Level  string `json:"level,omitempty" yaml:"level,omitempty"`
}

func DefaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		API: APIConfig{
			Listen:            DefaultAPIListen,
			ReadHeaderTimeout: Duration{Duration: 10 * time.Second},
			ShutdownTimeout:   Duration{Duration: 30 * time.Second},
		},
		Node: NodeConfig{
			Name: "local",
		},
		State: StateConfig{
			Backend: "file",
			Dir:     DefaultStateDir,
			Etcd: EtcdConfig{
				Prefix:      "/aifar-runtime",
				DialTimeout: Duration{Duration: 5 * time.Second},
			},
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
		SelfHeal: SelfHealConfig{
			Enabled:     boolPtr(true),
			MaxRestarts: 3,
			Backoff:     Duration{Duration: 10 * time.Second},
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
		Observability: ObservabilityConfig{
			MetricsEnabled: true,
		},
		Log: LogConfig{
			Format: "json",
			Level:  "info",
		},
	}
}

func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	config := DefaultRuntimeConfig()
	path = strings.TrimSpace(path)
	if path == "" {
		if err := ValidateRuntimeConfig(config); err != nil {
			return RuntimeConfig{}, err
		}
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
	if err := resolveRuntimeConfigSecrets(&config); err != nil {
		return RuntimeConfig{}, err
	}
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
	config.Node.Name = defaultString(config.Node.Name, defaults.Node.Name)
	trimStringMap(config.Node.Labels)
	config.State.Backend = strings.ToLower(defaultString(config.State.Backend, defaults.State.Backend))
	config.State.Dir = defaultString(config.State.Dir, defaults.State.Dir)
	config.State.Etcd.Prefix = defaultString(config.State.Etcd.Prefix, defaults.State.Etcd.Prefix)
	config.State.Etcd.DialTimeout = defaultDuration(config.State.Etcd.DialTimeout, defaults.State.Etcd.DialTimeout)
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
	config.SelfHeal.Enabled = defaultBoolPtr(config.SelfHeal.Enabled, true)
	if config.SelfHeal.MaxRestarts <= 0 {
		config.SelfHeal.MaxRestarts = defaults.SelfHeal.MaxRestarts
	}
	config.SelfHeal.Backoff = defaultDuration(config.SelfHeal.Backoff, defaults.SelfHeal.Backoff)
	config.Proxy.ReadHeaderTimeout = defaultDuration(config.Proxy.ReadHeaderTimeout, defaults.Proxy.ReadHeaderTimeout)
	config.Reconcile.Interval = defaultDuration(config.Reconcile.Interval, defaults.Reconcile.Interval)
	config.Health.DockerTimeout = defaultDuration(config.Health.DockerTimeout, defaults.Health.DockerTimeout)
	config.Security.BearerToken = strings.TrimSpace(config.Security.BearerToken)
	config.Security.BearerTokenFile = strings.TrimSpace(config.Security.BearerTokenFile)
	config.Security.TLSCertFile = strings.TrimSpace(config.Security.TLSCertFile)
	config.Security.TLSKeyFile = strings.TrimSpace(config.Security.TLSKeyFile)
	for i := range config.Security.RBAC.Tokens {
		token := &config.Security.RBAC.Tokens[i]
		token.Name = strings.TrimSpace(token.Name)
		token.Role = strings.ToLower(defaultString(token.Role, "admin"))
		token.Token = strings.TrimSpace(token.Token)
		token.TokenFile = strings.TrimSpace(token.TokenFile)
	}
	config.Log.Format = strings.ToLower(defaultString(config.Log.Format, defaults.Log.Format))
	config.Log.Level = strings.ToLower(defaultString(config.Log.Level, defaults.Log.Level))
	return config
}

func ValidateRuntimeConfig(config RuntimeConfig) error {
	if err := validateListenAddress("api.listen", config.API.Listen); err != nil {
		return err
	}
	if strings.TrimSpace(config.State.Dir) == "" {
		return errorsForField("state.dir", "must not be empty")
	}
	switch config.State.Backend {
	case "file":
	case "etcd":
		return errorsForField("state.backend", "etcd is reserved for clustered control-plane storage but is not implemented in this single-node runtime yet")
	default:
		return errorsForField("state.backend", `must be "file" or reserved value "etcd"`)
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
	if !validDNSLabel(config.Node.Name) {
		return errorsForField("node.name", "must use lowercase letters, digits, and '-'")
	}
	if err := validateConfigResourceSpec("node.capacity", config.Node.Capacity); err != nil {
		return err
	}
	if err := validateConfigResourceSpec("node.allocatable", config.Node.Allocatable); err != nil {
		return err
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
	if config.SelfHeal.Enabled == nil {
		return errorsForField("selfHeal.enabled", "must be set")
	}
	if config.SelfHeal.MaxRestarts <= 0 {
		return errorsForField("selfHeal.maxRestarts", "must be greater than zero")
	}
	if config.SelfHeal.Backoff.Duration <= 0 {
		return errorsForField("selfHeal.backoff", "must be greater than zero")
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
	if (config.Security.TLSCertFile == "") != (config.Security.TLSKeyFile == "") {
		return errorsForField("security.tlsCertFile/security.tlsKeyFile", "must be configured together")
	}
	if config.Security.BearerToken != "" && config.Security.RBAC.Enabled {
		return errorsForField("security.bearerToken", "must not be set when security.rbac.enabled is true")
	}
	if config.Security.BearerTokenFile != "" && config.Security.RBAC.Enabled {
		return errorsForField("security.bearerTokenFile", "must not be set when security.rbac.enabled is true")
	}
	for _, token := range config.Security.RBAC.Tokens {
		if token.Name == "" {
			return errorsForField("security.rbac.tokens.name", "must not be empty")
		}
		switch token.Role {
		case "admin", "operator", "viewer":
		default:
			return errorsForField("security.rbac.tokens.role", `must be "admin", "operator", or "viewer"`)
		}
		if token.Token == "" && token.TokenFile == "" {
			return errorsForField("security.rbac.tokens.token", "token or tokenFile is required")
		}
		if token.Token != "" && token.TokenFile != "" {
			return errorsForField("security.rbac.tokens.tokenFile", "must not be set with token")
		}
	}
	switch config.Log.Format {
	case "json", "text":
	default:
		return errorsForField("log.format", `must be "json" or "text"`)
	}
	switch config.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return errorsForField("log.level", `must be "debug", "info", "warn", or "error"`)
	}
	return nil
}

func resolveRuntimeConfigSecrets(config *RuntimeConfig) error {
	if config == nil {
		return nil
	}
	if config.Security.BearerToken != "" && config.Security.BearerTokenFile != "" {
		return errorsForField("security.bearerTokenFile", "must not be set with security.bearerToken")
	}
	if config.Security.BearerTokenFile == "" {
		return resolveRuntimeConfigRBACTokens(config)
	}
	data, err := os.ReadFile(config.Security.BearerTokenFile)
	if err != nil {
		return fmt.Errorf("read runtime config security.bearerTokenFile: %w", err)
	}
	config.Security.BearerToken = strings.TrimSpace(string(data))
	return resolveRuntimeConfigRBACTokens(config)
}

func resolveRuntimeConfigRBACTokens(config *RuntimeConfig) error {
	for i := range config.Security.RBAC.Tokens {
		token := &config.Security.RBAC.Tokens[i]
		if token.TokenFile == "" {
			continue
		}
		data, err := os.ReadFile(token.TokenFile)
		if err != nil {
			return fmt.Errorf("read runtime config security.rbac.tokens[%d].tokenFile: %w", i, err)
		}
		token.Token = strings.TrimSpace(string(data))
		token.TokenFile = ""
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

func boolPtr(value bool) *bool {
	return &value
}

func defaultBoolPtr(value *bool, fallback bool) *bool {
	if value != nil {
		return value
	}
	return boolPtr(fallback)
}

func validateConfigResourceSpec(field string, resources ResourceSpec) error {
	if resources.CPUs != "" && !cpuLimitRE.MatchString(strings.ToLower(strings.TrimSpace(resources.CPUs))) {
		return errorsForField(field+".cpus", "must be a positive decimal")
	}
	if resources.Memory != "" && !memoryLimitRE.MatchString(strings.ToLower(strings.TrimSpace(resources.Memory))) {
		return errorsForField(field+".memory", "must be a positive Docker memory value")
	}
	if resources.MemorySwap != "" && !memoryLimitRE.MatchString(strings.ToLower(strings.TrimSpace(resources.MemorySwap))) {
		return errorsForField(field+".memorySwap", "must be a positive Docker memory value")
	}
	if resources.PIDsLimit < 0 {
		return errorsForField(field+".pidsLimit", "must be >= 0")
	}
	return nil
}
