package runtimeagent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultAPIVersion = "aifar.io/v1"
	DefaultKind       = "Runtime"
	DefaultNamespace  = "default"
	DefaultNetwork    = "aifar-runtime"
	DefaultStateDir   = "/var/lib/aifar-runtime"
	RuntimeVersion    = "v0.1"
)

type Runtime struct {
	APIVersion string         `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty" yaml:"kind,omitempty"`
	Metadata   ObjectMeta     `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Spec       RuntimeSpec    `json:"spec,omitempty" yaml:"spec,omitempty"`
	Status     *RuntimeStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

type ObjectMeta struct {
	Name       string `json:"name,omitempty" yaml:"name,omitempty"`
	Namespace  string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Generation int64  `json:"generation,omitempty" yaml:"generation,omitempty"`
}

type RuntimeSpec struct {
	Network     string           `json:"network,omitempty" yaml:"network,omitempty"`
	Secrets     []SecretSpec     `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Deployments []DeploymentSpec `json:"deployments,omitempty" yaml:"deployments,omitempty"`
	Services    []ServiceSpec    `json:"services,omitempty" yaml:"services,omitempty"`
	Ingress     []IngressSpec    `json:"ingress,omitempty" yaml:"ingress,omitempty"`
}

type DeploymentSpec struct {
	Name             string                 `json:"name,omitempty" yaml:"name,omitempty"`
	ServiceName      string                 `json:"serviceName,omitempty" yaml:"serviceName,omitempty"`
	Image            string                 `json:"image,omitempty" yaml:"image,omitempty"`
	ImagePullSecrets []LocalObjectReference `json:"imagePullSecrets,omitempty" yaml:"imagePullSecrets,omitempty"`
	Replicas         *int                   `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	Strategy         DeploymentStrategy     `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	Selector         map[string]string      `json:"selector,omitempty" yaml:"selector,omitempty"`
	Ports            []ContainerPort        `json:"ports,omitempty" yaml:"ports,omitempty"`
	Env              map[string]string      `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFiles         []string               `json:"envFiles,omitempty" yaml:"envFiles,omitempty"`
	EnvFrom          []EnvFromSource        `json:"envFrom,omitempty" yaml:"envFrom,omitempty"`
	Volumes          []VolumeMount          `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Resources        ResourceSpec           `json:"resources,omitempty" yaml:"resources,omitempty"`
	HealthCheck      HealthCheckSpec        `json:"healthCheck,omitempty" yaml:"healthCheck,omitempty"`
	Entrypoint       []string               `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
	Command          []string               `json:"command,omitempty" yaml:"command,omitempty"`
	Labels           map[string]string      `json:"labels,omitempty" yaml:"labels,omitempty"`
	Revision         string                 `json:"revision,omitempty" yaml:"revision,omitempty"`
	PodRevision      string                 `json:"podRevision,omitempty" yaml:"podRevision,omitempty"`
}

type SecretSpec struct {
	Name       string            `json:"name,omitempty" yaml:"name,omitempty"`
	Type       string            `json:"type,omitempty" yaml:"type,omitempty"`
	StringData map[string]string `json:"stringData,omitempty" yaml:"stringData,omitempty"`
	Data       map[string]string `json:"data,omitempty" yaml:"data,omitempty"`
}

type LocalObjectReference struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

type DeploymentStrategy struct {
	Type          string                 `json:"type,omitempty" yaml:"type,omitempty"`
	RollingUpdate *RollingUpdateStrategy `json:"rollingUpdate,omitempty" yaml:"rollingUpdate,omitempty"`
}

type RollingUpdateStrategy struct {
	MaxUnavailable int `json:"maxUnavailable,omitempty" yaml:"maxUnavailable,omitempty"`
	MaxSurge       int `json:"maxSurge,omitempty" yaml:"maxSurge,omitempty"`
}

type ContainerPort struct {
	Name          string `json:"name,omitempty" yaml:"name,omitempty"`
	ContainerPort int    `json:"containerPort" yaml:"containerPort"`
}

type EnvFromSource struct {
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

type VolumeMount struct {
	Source   string `json:"source,omitempty" yaml:"source,omitempty"`
	Target   string `json:"target,omitempty" yaml:"target,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
}

type ResourceSpec struct {
	CPUs       string `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	Memory     string `json:"memory,omitempty" yaml:"memory,omitempty"`
	MemorySwap string `json:"memorySwap,omitempty" yaml:"memorySwap,omitempty"`
	PIDsLimit  int    `json:"pidsLimit,omitempty" yaml:"pidsLimit,omitempty"`
}

type HealthCheckSpec struct {
	Command     string       `json:"command,omitempty" yaml:"command,omitempty"`
	HTTPGet     *HTTPGetSpec `json:"httpGet,omitempty" yaml:"httpGet,omitempty"`
	Interval    string       `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timeout     string       `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retries     int          `json:"retries,omitempty" yaml:"retries,omitempty"`
	StartPeriod string       `json:"startPeriod,omitempty" yaml:"startPeriod,omitempty"`
}

type HTTPGetSpec struct {
	Path string      `json:"path,omitempty" yaml:"path,omitempty"`
	Port IntOrString `json:"port,omitempty" yaml:"port,omitempty"`
}

type ServiceSpec struct {
	Name           string            `json:"name,omitempty" yaml:"name,omitempty"`
	Selector       map[string]string `json:"selector,omitempty" yaml:"selector,omitempty"`
	Port           int               `json:"port,omitempty" yaml:"port,omitempty"`
	TargetPort     IntOrString       `json:"targetPort,omitempty" yaml:"targetPort,omitempty"`
	ListenPort     int               `json:"listenPort,omitempty" yaml:"listenPort,omitempty"`
	Protocol       string            `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	AffinityPolicy string            `json:"affinityPolicy,omitempty" yaml:"affinityPolicy,omitempty"`
}

type IngressSpec struct {
	Name       string         `json:"name,omitempty" yaml:"name,omitempty"`
	Provider   string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Host       string         `json:"host,omitempty" yaml:"host,omitempty"`
	ListenPort int            `json:"listenPort,omitempty" yaml:"listenPort,omitempty"`
	TLS        *IngressTLS    `json:"tls,omitempty" yaml:"tls,omitempty"`
	Routes     []IngressRoute `json:"routes,omitempty" yaml:"routes,omitempty"`
}

type IngressTLS struct {
	CertFile string `json:"certFile,omitempty" yaml:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty" yaml:"keyFile,omitempty"`
}

type IngressRoute struct {
	Path        string      `json:"path,omitempty" yaml:"path,omitempty"`
	ServiceName string      `json:"serviceName,omitempty" yaml:"serviceName,omitempty"`
	ServicePort IntOrString `json:"servicePort,omitempty" yaml:"servicePort,omitempty"`
}

type RuntimeStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty" yaml:"observedGeneration,omitempty"`
	Phase              string             `json:"phase,omitempty" yaml:"phase,omitempty"`
	Conditions         []Condition        `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Deployments        []DeploymentStatus `json:"deployments,omitempty" yaml:"deployments,omitempty"`
	Services           []ServiceStatus    `json:"services,omitempty" yaml:"services,omitempty"`
	Ingress            []IngressStatus    `json:"ingress,omitempty" yaml:"ingress,omitempty"`
	LastTransitionTime string             `json:"lastTransitionTime,omitempty" yaml:"lastTransitionTime,omitempty"`
}

type Condition struct {
	Type               string `json:"type,omitempty" yaml:"type,omitempty"`
	Status             string `json:"status,omitempty" yaml:"status,omitempty"`
	Reason             string `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message            string `json:"message,omitempty" yaml:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty" yaml:"lastTransitionTime,omitempty"`
}

type DeploymentStatus struct {
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	Ready    int    `json:"ready,omitempty" yaml:"ready,omitempty"`
	Replicas int    `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	Image    string `json:"image,omitempty" yaml:"image,omitempty"`
	Revision string `json:"revision,omitempty" yaml:"revision,omitempty"`
	Phase    string `json:"phase,omitempty" yaml:"phase,omitempty"`
	Message  string `json:"message,omitempty" yaml:"message,omitempty"`
}

type ServiceStatus struct {
	Name       string     `json:"name,omitempty" yaml:"name,omitempty"`
	ListenPort int        `json:"listenPort,omitempty" yaml:"listenPort,omitempty"`
	Port       int        `json:"port,omitempty" yaml:"port,omitempty"`
	TargetPort string     `json:"targetPort,omitempty" yaml:"targetPort,omitempty"`
	Endpoints  []Endpoint `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
	Phase      string     `json:"phase,omitempty" yaml:"phase,omitempty"`
}

type IngressStatus struct {
	Name       string `json:"name,omitempty" yaml:"name,omitempty"`
	Host       string `json:"host,omitempty" yaml:"host,omitempty"`
	ListenPort int    `json:"listenPort,omitempty" yaml:"listenPort,omitempty"`
	Phase      string `json:"phase,omitempty" yaml:"phase,omitempty"`
}

type Endpoint struct {
	Container string `json:"container,omitempty" yaml:"container,omitempty"`
	Address   string `json:"address,omitempty" yaml:"address,omitempty"`
}

type IntOrString struct {
	IntVal int
	StrVal string
}

func FromInt(value int) IntOrString {
	return IntOrString{IntVal: value}
}

func FromString(value string) IntOrString {
	return IntOrString{StrVal: strings.TrimSpace(value)}
}

func (v IntOrString) IsZero() bool {
	return v.IntVal == 0 && strings.TrimSpace(v.StrVal) == ""
}

func (v IntOrString) String() string {
	if v.StrVal != "" {
		return v.StrVal
	}
	if v.IntVal != 0 {
		return strconv.Itoa(v.IntVal)
	}
	return ""
}

func (v IntOrString) MarshalJSON() ([]byte, error) {
	if v.StrVal != "" {
		return json.Marshal(v.StrVal)
	}
	return json.Marshal(v.IntVal)
}

func (v *IntOrString) UnmarshalJSON(data []byte) error {
	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*v = FromInt(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	if parsed, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
		*v = FromInt(parsed)
		return nil
	}
	*v = FromString(text)
	return nil
}

func (v IntOrString) MarshalYAML() (any, error) {
	if v.StrVal != "" {
		return v.StrVal, nil
	}
	return v.IntVal, nil
}

func (v *IntOrString) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!int" {
			parsed, err := strconv.Atoi(node.Value)
			if err != nil {
				return err
			}
			*v = FromInt(parsed)
			return nil
		}
		if parsed, err := strconv.Atoi(strings.TrimSpace(node.Value)); err == nil {
			*v = FromInt(parsed)
			return nil
		}
		*v = FromString(node.Value)
		return nil
	default:
		return fmt.Errorf("expected scalar int or string, got YAML node kind %d", node.Kind)
	}
}

type RuntimeEvent struct {
	Time      string `json:"time"`
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type RuntimeKey struct {
	Namespace string
	Name      string
}

func (k RuntimeKey) String() string {
	return k.Namespace + "/" + k.Name
}

func KeyForRuntime(runtime Runtime) RuntimeKey {
	runtime = NormalizeRuntime(runtime)
	return RuntimeKey{Namespace: runtime.Metadata.Namespace, Name: runtime.Metadata.Name}
}

func NormalizeRuntime(runtime Runtime) Runtime {
	if strings.TrimSpace(runtime.APIVersion) == "" {
		runtime.APIVersion = DefaultAPIVersion
	}
	if strings.TrimSpace(runtime.Kind) == "" {
		runtime.Kind = DefaultKind
	}
	runtime.Metadata.Name = strings.TrimSpace(runtime.Metadata.Name)
	runtime.Metadata.Namespace = strings.TrimSpace(runtime.Metadata.Namespace)
	if runtime.Metadata.Namespace == "" {
		runtime.Metadata.Namespace = DefaultNamespace
	}
	if strings.TrimSpace(runtime.Spec.Network) == "" {
		runtime.Spec.Network = DefaultNetwork
	}
	for i := range runtime.Spec.Secrets {
		secret := &runtime.Spec.Secrets[i]
		secret.Name = strings.TrimSpace(secret.Name)
		secret.Type = strings.ToLower(defaultSecretType(secret.Type))
		trimStringMap(secret.StringData)
		trimStringMap(secret.Data)
	}
	for i := range runtime.Spec.Deployments {
		deployment := &runtime.Spec.Deployments[i]
		deployment.Name = strings.TrimSpace(deployment.Name)
		if deployment.Name == "" {
			deployment.Name = strings.TrimSpace(deployment.ServiceName)
		}
		deployment.ServiceName = strings.TrimSpace(deployment.ServiceName)
		deployment.Image = strings.TrimSpace(deployment.Image)
		deployment.Revision = strings.TrimSpace(deployment.Revision)
		if deployment.Revision == "" {
			deployment.Revision = strings.TrimSpace(deployment.PodRevision)
		}
		if deployment.Revision == "" {
			deployment.Revision = "current"
		}
		if deployment.Replicas == nil {
			value := 1
			deployment.Replicas = &value
		}
		deployment.Strategy.Type = strings.ToLower(strings.TrimSpace(deployment.Strategy.Type))
		if deployment.Strategy.Type == "" {
			deployment.Strategy.Type = "rollingupdate"
		}
		if deployment.Strategy.Type == "rollingupdate" && deployment.Strategy.RollingUpdate == nil {
			deployment.Strategy.RollingUpdate = &RollingUpdateStrategy{MaxUnavailable: 0, MaxSurge: 1}
		}
		for j := range deployment.ImagePullSecrets {
			deployment.ImagePullSecrets[j].Name = strings.TrimSpace(deployment.ImagePullSecrets[j].Name)
		}
		if deployment.Selector == nil {
			deployment.Selector = map[string]string{}
		}
		if strings.TrimSpace(deployment.Selector["app"]) == "" && deployment.Name != "" {
			deployment.Selector["app"] = deployment.Name
		}
		trimStringMap(deployment.Selector)
		trimStringMap(deployment.Env)
	}
	for i := range runtime.Spec.Services {
		service := &runtime.Spec.Services[i]
		service.Name = strings.TrimSpace(service.Name)
		if service.Selector == nil {
			service.Selector = map[string]string{}
		}
		if len(service.Selector) == 0 && service.Name != "" {
			service.Selector["app"] = service.Name
		}
		trimStringMap(service.Selector)
		if service.ListenPort == 0 {
			service.ListenPort = service.Port
		}
		if service.TargetPort.IsZero() {
			service.TargetPort = FromInt(service.Port)
		}
		if strings.TrimSpace(service.Protocol) == "" {
			service.Protocol = "http"
		}
		if strings.TrimSpace(service.AffinityPolicy) == "" {
			service.AffinityPolicy = "none"
		}
	}
	for i := range runtime.Spec.Ingress {
		ingress := &runtime.Spec.Ingress[i]
		ingress.Name = strings.TrimSpace(ingress.Name)
		if strings.TrimSpace(ingress.Provider) == "" {
			ingress.Provider = "builtin"
		}
		if strings.TrimSpace(ingress.Host) == "" {
			ingress.Host = "*"
		}
		for j := range ingress.Routes {
			route := &ingress.Routes[j]
			route.Path = cleanIngressPath(route.Path)
			route.ServiceName = strings.TrimSpace(route.ServiceName)
			if route.ServicePort.IsZero() {
				if service, ok := serviceByName(runtime, route.ServiceName); ok {
					route.ServicePort = FromInt(service.Port)
				}
			}
		}
	}
	return runtime
}

func ParseRuntimeDocument(data []byte) (Runtime, error) {
	if err := rejectRenderedRuntimeHazards(data); err != nil {
		return Runtime{}, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Runtime{}, err
	}
	if hasMapKey(raw, "status") {
		return Runtime{}, errors.New("rendered Runtime must not contain status")
	}
	if hasForbiddenField(raw, "registryProjections") {
		return Runtime{}, errors.New("rendered Runtime must not contain registryProjections")
	}
	if isLegacyRuntimeDocument(raw) {
		var legacy legacyRuntimeSpec
		if err := yaml.Unmarshal(data, &legacy); err != nil {
			return Runtime{}, err
		}
		runtime := legacy.ToRuntime()
		if err := ValidateRuntime(runtime); err != nil {
			return Runtime{}, err
		}
		return NormalizeRuntime(runtime), nil
	}
	var runtime Runtime
	if err := yaml.Unmarshal(data, &runtime); err != nil {
		return Runtime{}, err
	}
	runtime = NormalizeRuntime(runtime)
	if err := ValidateRuntime(runtime); err != nil {
		return Runtime{}, err
	}
	return runtime, nil
}

func MustJSONRuntime(runtime Runtime) []byte {
	data, _ := json.MarshalIndent(NormalizeRuntime(runtime), "", "  ")
	return append(data, '\n')
}

func ValidateRuntime(runtime Runtime) error {
	runtime = NormalizeRuntime(runtime)
	if runtime.APIVersion != DefaultAPIVersion {
		return fmt.Errorf("unsupported apiVersion %q", runtime.APIVersion)
	}
	if runtime.Kind != DefaultKind {
		return fmt.Errorf("unsupported kind %q", runtime.Kind)
	}
	if !validDNSLabel(runtime.Metadata.Name) {
		return fmt.Errorf("metadata.name %q must use lowercase letters, digits, and '-'", runtime.Metadata.Name)
	}
	if !validDNSLabel(runtime.Metadata.Namespace) {
		return fmt.Errorf("metadata.namespace %q must use lowercase letters, digits, and '-'", runtime.Metadata.Namespace)
	}
	if strings.TrimSpace(runtime.Spec.Network) == "" {
		return errors.New("spec.network is required")
	}
	if len(runtime.Spec.Deployments) == 0 {
		return errors.New("spec.deployments is required")
	}
	if len(runtime.Spec.Services) == 0 {
		return errors.New("spec.services is required")
	}
	secrets := map[string]SecretSpec{}
	for _, secret := range runtime.Spec.Secrets {
		if !validDNSLabel(secret.Name) {
			return fmt.Errorf("secret name %q must use lowercase letters, digits, and '-'", secret.Name)
		}
		if _, exists := secrets[secret.Name]; exists {
			return fmt.Errorf("duplicate secret %q", secret.Name)
		}
		switch secret.Type {
		case "opaque", "registry-auth", "dockerconfigjson":
		default:
			return fmt.Errorf("secret %s type %q is not supported", secret.Name, secret.Type)
		}
		if len(secret.StringData) == 0 && len(secret.Data) == 0 {
			return fmt.Errorf("secret %s data is required", secret.Name)
		}
		for key, value := range secret.Data {
			if _, err := base64.StdEncoding.DecodeString(value); err != nil {
				return fmt.Errorf("secret %s data[%s] must be base64 encoded", secret.Name, key)
			}
		}
		secrets[secret.Name] = secret
	}
	deployments := map[string]DeploymentSpec{}
	for _, deployment := range runtime.Spec.Deployments {
		if !validDNSLabel(deployment.Name) {
			return fmt.Errorf("deployment name %q must use lowercase letters, digits, and '-'", deployment.Name)
		}
		if _, exists := deployments[deployment.Name]; exists {
			return fmt.Errorf("duplicate deployment %q", deployment.Name)
		}
		if strings.TrimSpace(deployment.Image) == "" {
			return fmt.Errorf("deployment %s image is required", deployment.Name)
		}
		if replicas := deploymentReplicas(deployment); replicas < 0 {
			return fmt.Errorf("deployment %s replicas must be >= 0", deployment.Name)
		}
		switch deployment.Strategy.Type {
		case "rollingupdate", "recreate":
		default:
			return fmt.Errorf("deployment %s strategy.type %q is not supported", deployment.Name, deployment.Strategy.Type)
		}
		if deployment.Strategy.RollingUpdate != nil {
			if deployment.Strategy.RollingUpdate.MaxUnavailable < 0 || deployment.Strategy.RollingUpdate.MaxSurge < 0 {
				return fmt.Errorf("deployment %s rollingUpdate maxUnavailable and maxSurge must be >= 0", deployment.Name)
			}
			if deployment.Strategy.Type == "rollingupdate" && deployment.Strategy.RollingUpdate.MaxUnavailable == 0 && deployment.Strategy.RollingUpdate.MaxSurge == 0 {
				return fmt.Errorf("deployment %s rollingUpdate requires maxSurge or maxUnavailable", deployment.Name)
			}
		}
		for _, ref := range deployment.ImagePullSecrets {
			if ref.Name == "" {
				return fmt.Errorf("deployment %s imagePullSecrets name is required", deployment.Name)
			}
			secret, ok := secrets[ref.Name]
			if !ok {
				return fmt.Errorf("deployment %s imagePullSecret %s is not defined", deployment.Name, ref.Name)
			}
			if secret.Type != "registry-auth" && secret.Type != "dockerconfigjson" {
				return fmt.Errorf("deployment %s imagePullSecret %s must be registry-auth or dockerconfigjson", deployment.Name, ref.Name)
			}
		}
		for _, source := range deployment.EnvFrom {
			if strings.EqualFold(source.Type, "secret") {
				if _, ok := secrets[source.Name]; !ok {
					return fmt.Errorf("deployment %s envFrom secret %s is not defined", deployment.Name, source.Name)
				}
			}
		}
		if err := validateResourceSpec(deployment.Name, deployment.Resources); err != nil {
			return err
		}
		if err := validateHealthCheckSpec(deployment.Name, deployment.HealthCheck); err != nil {
			return err
		}
		for _, volume := range deployment.Volumes {
			if strings.TrimSpace(volume.Source) == "" || strings.TrimSpace(volume.Target) == "" {
				return fmt.Errorf("deployment %s volume source and target are required", deployment.Name)
			}
			if !strings.HasPrefix(volume.Target, "/") {
				return fmt.Errorf("deployment %s volume target %q must be absolute", deployment.Name, volume.Target)
			}
		}
		for _, port := range deployment.Ports {
			if strings.TrimSpace(port.Name) != "" && !validPortName(port.Name) {
				return fmt.Errorf("deployment %s port name %q is invalid", deployment.Name, port.Name)
			}
			if port.ContainerPort <= 0 {
				return fmt.Errorf("deployment %s containerPort must be positive", deployment.Name)
			}
		}
		deployments[deployment.Name] = deployment
	}
	serviceListenPorts := map[int]string{}
	services := map[string]ServiceSpec{}
	for _, service := range runtime.Spec.Services {
		if !validDNSLabel(service.Name) {
			return fmt.Errorf("service name %q must use lowercase letters, digits, and '-'", service.Name)
		}
		if _, exists := services[service.Name]; exists {
			return fmt.Errorf("duplicate service %q", service.Name)
		}
		if len(service.Selector) == 0 {
			return fmt.Errorf("service %s selector is required", service.Name)
		}
		if service.Port <= 0 || service.ListenPort <= 0 {
			return fmt.Errorf("service %s port and listenPort must be positive", service.Name)
		}
		if service.TargetPort.IsZero() {
			return fmt.Errorf("service %s targetPort is required", service.Name)
		}
		if previous := serviceListenPorts[service.ListenPort]; previous != "" {
			return fmt.Errorf("listenPort %d is used by both services %s and %s", service.ListenPort, previous, service.Name)
		}
		serviceListenPorts[service.ListenPort] = service.Name
		if protocol := strings.ToLower(strings.TrimSpace(service.Protocol)); protocol != "http" {
			return fmt.Errorf("service %s protocol %q is not supported in v0.1", service.Name, service.Protocol)
		}
		matches := matchingDeployments(runtime, service.Selector)
		if len(matches) == 0 {
			return fmt.Errorf("service %s selector does not match any deployment", service.Name)
		}
		if _, err := resolveServiceTargetPort(service, matches); err != nil {
			return err
		}
		services[service.Name] = service
	}
	for _, ingress := range runtime.Spec.Ingress {
		if !validDNSLabel(ingress.Name) {
			return fmt.Errorf("ingress name %q must use lowercase letters, digits, and '-'", ingress.Name)
		}
		if strings.ToLower(strings.TrimSpace(ingress.Provider)) != "builtin" {
			return fmt.Errorf("ingress %s provider %q is not supported in v0.1", ingress.Name, ingress.Provider)
		}
		if ingress.ListenPort <= 0 {
			return fmt.Errorf("ingress %s listenPort must be positive", ingress.Name)
		}
		if conflict := serviceListenPorts[ingress.ListenPort]; conflict != "" {
			return fmt.Errorf("listenPort %d is used by service %s and ingress %s", ingress.ListenPort, conflict, ingress.Name)
		}
		if len(ingress.Routes) == 0 {
			return fmt.Errorf("ingress %s routes are required", ingress.Name)
		}
		for _, route := range ingress.Routes {
			if !strings.HasPrefix(route.Path, "/") {
				return fmt.Errorf("ingress %s route path %q must start with /", ingress.Name, route.Path)
			}
			service, ok := services[route.ServiceName]
			if !ok {
				return fmt.Errorf("ingress %s route references missing service %s", ingress.Name, route.ServiceName)
			}
			if !route.ServicePort.IsZero() && route.ServicePort.String() != strconv.Itoa(service.Port) && route.ServicePort.String() != service.Name {
				return fmt.Errorf("ingress %s route servicePort %s does not match service %s port %d", ingress.Name, route.ServicePort.String(), service.Name, service.Port)
			}
		}
	}
	return nil
}

func defaultSecretType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "opaque"
	}
	return value
}

func deploymentReplicas(deployment DeploymentSpec) int {
	if deployment.Replicas == nil {
		return 1
	}
	return *deployment.Replicas
}

func secretByName(runtime Runtime, name string) (SecretSpec, bool) {
	name = strings.TrimSpace(name)
	for _, secret := range runtime.Spec.Secrets {
		if secret.Name == name {
			return secret, true
		}
	}
	return SecretSpec{}, false
}

func runtimeKey(namespace, name string) RuntimeKey {
	key := RuntimeKey{
		Namespace: strings.TrimSpace(namespace),
		Name:      strings.TrimSpace(name),
	}
	if key.Namespace == "" {
		key.Namespace = DefaultNamespace
	}
	return key
}

func serviceByName(runtime Runtime, name string) (ServiceSpec, bool) {
	name = strings.TrimSpace(name)
	for _, service := range runtime.Spec.Services {
		if service.Name == name {
			return service, true
		}
	}
	return ServiceSpec{}, false
}

func deploymentByName(runtime Runtime, name string) (DeploymentSpec, bool) {
	name = strings.TrimSpace(name)
	for _, deployment := range runtime.Spec.Deployments {
		if deployment.Name == name {
			return deployment, true
		}
	}
	return DeploymentSpec{}, false
}

func matchingDeployments(runtime Runtime, selector map[string]string) []DeploymentSpec {
	out := []DeploymentSpec{}
	for _, deployment := range runtime.Spec.Deployments {
		if labelsMatch(deployment.Selector, selector) {
			out = append(out, deployment)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func labelsMatch(labels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, want := range selector {
		if strings.TrimSpace(labels[key]) != strings.TrimSpace(want) {
			return false
		}
	}
	return true
}

func resolveServiceTargetPort(service ServiceSpec, deployments []DeploymentSpec) (int, error) {
	if service.TargetPort.IntVal > 0 {
		return service.TargetPort.IntVal, nil
	}
	name := strings.TrimSpace(service.TargetPort.StrVal)
	if name == "" {
		return 0, fmt.Errorf("service %s targetPort is required", service.Name)
	}
	for _, deployment := range deployments {
		for _, port := range deployment.Ports {
			if port.Name == name && port.ContainerPort > 0 {
				return port.ContainerPort, nil
			}
		}
	}
	return 0, fmt.Errorf("service %s targetPort %q does not match selected deployment ports", service.Name, name)
}

func cleanIngressPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func validDNSLabel(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	return dnsLabelRE.MatchString(value)
}

func validPortName(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	return dnsLabelRE.MatchString(value)
}

var dnsLabelRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var memoryLimitRE = regexp.MustCompile(`^[1-9][0-9]*(b|k|m|g|t|kb|mb|gb|tb|ki|mi|gi|ti|kib|mib|gib|tib)?$`)
var cpuLimitRE = regexp.MustCompile(`^([1-9][0-9]*|0\.[0-9]*[1-9][0-9]*|[1-9][0-9]*\.[0-9]+)$`)

func validateResourceSpec(deployment string, resources ResourceSpec) error {
	if resources.CPUs != "" && !cpuLimitRE.MatchString(strings.ToLower(strings.TrimSpace(resources.CPUs))) {
		return fmt.Errorf("deployment %s resources.cpus %q must be a positive decimal", deployment, resources.CPUs)
	}
	if resources.Memory != "" && !memoryLimitRE.MatchString(strings.ToLower(strings.TrimSpace(resources.Memory))) {
		return fmt.Errorf("deployment %s resources.memory %q must be a positive Docker memory value", deployment, resources.Memory)
	}
	if resources.MemorySwap != "" && !memoryLimitRE.MatchString(strings.ToLower(strings.TrimSpace(resources.MemorySwap))) {
		return fmt.Errorf("deployment %s resources.memorySwap %q must be a positive Docker memory value", deployment, resources.MemorySwap)
	}
	if resources.PIDsLimit < 0 {
		return fmt.Errorf("deployment %s resources.pidsLimit must be >= 0", deployment)
	}
	return nil
}

func validateHealthCheckSpec(deployment string, health HealthCheckSpec) error {
	for field, value := range map[string]string{
		"interval":    health.Interval,
		"timeout":     health.Timeout,
		"startPeriod": health.StartPeriod,
	} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("deployment %s healthCheck.%s %q must be a valid duration", deployment, field, value)
		}
	}
	if health.Retries < 0 {
		return fmt.Errorf("deployment %s healthCheck.retries must be >= 0", deployment)
	}
	return nil
}

func trimStringMap(values map[string]string) {
	for key, value := range values {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" {
			delete(values, key)
			continue
		}
		if trimmedKey != key {
			delete(values, key)
		}
		values[trimmedKey] = trimmedValue
	}
}

func rejectRenderedRuntimeHazards(data []byte) error {
	text := string(data)
	for _, token := range []string{"{{", "}}", "${", "<%"} {
		if strings.Contains(text, token) {
			return fmt.Errorf("rendered Runtime contains unresolved template token %q", token)
		}
	}
	return nil
}

func hasMapKey(values map[string]any, key string) bool {
	for existing := range values {
		if existing == key {
			return true
		}
	}
	return false
}

func hasForbiddenField(value any, field string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == field || hasForbiddenField(nested, field) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if hasForbiddenField(nested, field) {
				return true
			}
		}
	}
	return false
}

func isLegacyRuntimeDocument(raw map[string]any) bool {
	if _, ok := raw["spec"]; ok {
		return false
	}
	if _, ok := raw["instanceId"]; ok {
		return true
	}
	_, hasServices := raw["services"]
	_, hasDeployments := raw["deployments"]
	return hasServices && hasDeployments
}

type legacyRuntimeSpec struct {
	InstanceID  string                 `json:"instanceId" yaml:"instanceId"`
	Network     string                 `json:"network" yaml:"network"`
	Deployments []legacyDeploymentSpec `json:"deployments" yaml:"deployments"`
	Services    []legacyServiceSpec    `json:"services" yaml:"services"`
	Ingress     legacyIngressSpec      `json:"ingress" yaml:"ingress"`
}

type legacyDeploymentSpec struct {
	ServiceName    string            `json:"serviceName" yaml:"serviceName"`
	DeploymentName string            `json:"deploymentName" yaml:"deploymentName"`
	Image          string            `json:"image" yaml:"image"`
	PodRevision    string            `json:"podRevision" yaml:"podRevision"`
	Replicas       int               `json:"replicas" yaml:"replicas"`
	Ports          []ContainerPort   `json:"ports" yaml:"ports"`
	EnvFiles       []string          `json:"envFiles" yaml:"envFiles"`
	Volumes        []VolumeMount     `json:"volumes" yaml:"volumes"`
	Resources      ResourceSpec      `json:"resources" yaml:"resources"`
	HealthCheck    HealthCheckSpec   `json:"healthCheck" yaml:"healthCheck"`
	Entrypoint     []string          `json:"entrypoint" yaml:"entrypoint"`
	Command        []string          `json:"command" yaml:"command"`
	Environment    map[string]string `json:"environment" yaml:"environment"`
	Labels         map[string]string `json:"labels" yaml:"labels"`
}

type legacyServiceSpec struct {
	Name           string `json:"name" yaml:"name"`
	Port           int    `json:"port" yaml:"port"`
	ListenPort     int    `json:"listenPort" yaml:"listenPort"`
	TargetPort     int    `json:"targetPort" yaml:"targetPort"`
	AffinityPolicy string `json:"affinityPolicy" yaml:"affinityPolicy"`
}

type legacyIngressSpec struct {
	GatewayService string `json:"gatewayService" yaml:"gatewayService"`
	WebService     string `json:"webService" yaml:"webService"`
	GatewayPort    int    `json:"gatewayPort" yaml:"gatewayPort"`
	WebPort        int    `json:"webPort" yaml:"webPort"`
}

func (legacy legacyRuntimeSpec) ToRuntime() Runtime {
	name := strings.TrimSpace(legacy.InstanceID)
	if name == "" {
		name = "default"
	}
	runtime := Runtime{
		APIVersion: DefaultAPIVersion,
		Kind:       DefaultKind,
		Metadata: ObjectMeta{
			Name:      sanitizeLegacyName(name),
			Namespace: DefaultNamespace,
		},
		Spec: RuntimeSpec{Network: legacy.Network},
	}
	for _, service := range legacy.Services {
		if service.ListenPort == 0 {
			service.ListenPort = service.Port
		}
		if service.TargetPort == 0 {
			service.TargetPort = service.Port
		}
		runtime.Spec.Services = append(runtime.Spec.Services, ServiceSpec{
			Name:           sanitizeLegacyName(service.Name),
			Selector:       map[string]string{"app": sanitizeLegacyName(service.Name)},
			Port:           service.Port,
			ListenPort:     service.ListenPort,
			TargetPort:     FromInt(service.TargetPort),
			AffinityPolicy: service.AffinityPolicy,
		})
	}
	for _, deployment := range legacy.Deployments {
		name := sanitizeLegacyName(deployment.ServiceName)
		if name == "" {
			name = sanitizeLegacyName(deployment.DeploymentName)
		}
		replicas := deployment.Replicas
		if replicas == 0 {
			replicas = 1
		}
		runtime.Spec.Deployments = append(runtime.Spec.Deployments, DeploymentSpec{
			Name:        name,
			Image:       deployment.Image,
			Replicas:    &replicas,
			Selector:    map[string]string{"app": name},
			Ports:       deployment.Ports,
			Env:         deployment.Environment,
			EnvFiles:    deployment.EnvFiles,
			Volumes:     deployment.Volumes,
			Resources:   deployment.Resources,
			HealthCheck: deployment.HealthCheck,
			Entrypoint:  deployment.Entrypoint,
			Command:     deployment.Command,
			Labels:      deployment.Labels,
			Revision:    deployment.PodRevision,
		})
	}
	if legacy.Ingress.WebPort > 0 && legacy.Ingress.WebService != "" {
		webService := sanitizeLegacyName(legacy.Ingress.WebService)
		routes := []IngressRoute{{Path: "/", ServiceName: webService}}
		if legacy.Ingress.GatewayService != "" {
			routes = append([]IngressRoute{{Path: "/api", ServiceName: sanitizeLegacyName(legacy.Ingress.GatewayService)}}, routes...)
		}
		runtime.Spec.Ingress = append(runtime.Spec.Ingress, IngressSpec{
			Name:       "public",
			Provider:   "builtin",
			Host:       "*",
			ListenPort: legacy.Ingress.WebPort,
			Routes:     routes,
		})
	}
	return NormalizeRuntime(runtime)
}

func sanitizeLegacyName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		case r == '_', r == '.', r == '/':
			b.WriteByte('-')
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	return out
}
