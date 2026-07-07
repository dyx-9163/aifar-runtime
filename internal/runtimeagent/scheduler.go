package runtimeagent

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type RuntimeScheduler interface {
	AdmitRuntime(runtime Runtime) (Runtime, error)
	AssignRuntime(runtime Runtime) Runtime
	ResourceSnapshot() ResourceSnapshot
}

type ResourceSnapshot struct {
	NodeName    string          `json:"nodeName,omitempty" yaml:"nodeName,omitempty"`
	Runtimes    int             `json:"runtimes,omitempty" yaml:"runtimes,omitempty"`
	Allocatable ResourceRequest `json:"allocatable,omitempty" yaml:"allocatable,omitempty"`
	Requested   ResourceRequest `json:"requested,omitempty" yaml:"requested,omitempty"`
	Available   ResourceRequest `json:"available,omitempty" yaml:"available,omitempty"`
}

type ResourceRequest struct {
	MilliCPUs   int64 `json:"milliCPUs,omitempty" yaml:"milliCPUs,omitempty"`
	MemoryBytes int64 `json:"memoryBytes,omitempty" yaml:"memoryBytes,omitempty"`
	PIDs        int64 `json:"pids,omitempty" yaml:"pids,omitempty"`
}

type SingleNodeScheduler struct {
	manager *Manager
}

func NewSingleNodeScheduler(manager *Manager) *SingleNodeScheduler {
	return &SingleNodeScheduler{manager: manager}
}

func (s *SingleNodeScheduler) AdmitRuntime(runtime Runtime) (Runtime, error) {
	runtime = s.AssignRuntime(runtime)
	if err := s.manager.validateRuntimeNode(runtime); err != nil {
		return Runtime{}, err
	}
	key := KeyForRuntime(runtime)
	existing := s.manager.runtimesForScheduling(key)
	if err := validateGlobalPortConflicts(runtime, existing); err != nil {
		return Runtime{}, err
	}
	if err := validateNodeCapacity(runtime, existing, s.manager.config.Node); err != nil {
		return Runtime{}, err
	}
	return runtime, nil
}

func (s *SingleNodeScheduler) AssignRuntime(runtime Runtime) Runtime {
	runtime = NormalizeRuntime(runtime)
	if strings.TrimSpace(runtime.Spec.NodeName) == "" {
		runtime.Spec.NodeName = s.manager.config.Node.Name
	}
	return runtime
}

func (s *SingleNodeScheduler) ResourceSnapshot() ResourceSnapshot {
	runtimes := s.manager.runtimesForScheduling(RuntimeKey{})
	return resourceSnapshotFor(s.manager.config.Node, runtimes)
}

func (m *Manager) AdmitRuntime(runtime Runtime) (Runtime, error) {
	return m.scheduler.AdmitRuntime(runtime)
}

func (m *Manager) AssignRuntime(runtime Runtime) Runtime {
	return m.scheduler.AssignRuntime(runtime)
}

func (m *Manager) ResourceSnapshot() ResourceSnapshot {
	return m.scheduler.ResourceSnapshot()
}

func (m *Manager) runtimesForScheduling(exclude RuntimeKey) []Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	runtimes := make([]Runtime, 0, len(m.specs))
	for _, key := range sortedRuntimeKeys(m.specs) {
		runtime := m.specs[key]
		if exclude.Name != "" && KeyForRuntime(runtime) == exclude {
			continue
		}
		runtimes = append(runtimes, runtime)
	}
	return runtimes
}

func validateGlobalPortConflicts(candidate Runtime, existing []Runtime) error {
	candidate = NormalizeRuntime(candidate)
	candidateKey := KeyForRuntime(candidate)
	servicePorts := map[int]portClaim{}
	ingressClaims := map[int][]ingressPortClaim{}
	for _, runtime := range existing {
		runtime = NormalizeRuntime(runtime)
		key := KeyForRuntime(runtime)
		for _, service := range runtime.Spec.Services {
			servicePorts[service.ListenPort] = portClaim{Key: key, Kind: "service", Name: service.Name, Port: service.ListenPort}
		}
		for _, ingress := range runtime.Spec.Ingress {
			for _, route := range ingress.Routes {
				ingressClaims[ingress.ListenPort] = append(ingressClaims[ingress.ListenPort], ingressPortClaim{
					Key:     key,
					Ingress: ingress.Name,
					Port:    ingress.ListenPort,
					Host:    ingress.Host,
					Path:    route.Path,
					Service: route.ServiceName,
				})
			}
		}
	}
	for _, service := range candidate.Spec.Services {
		if claim, ok := servicePorts[service.ListenPort]; ok {
			return fmt.Errorf("service %s/%s listenPort %d conflicts with %s %s/%s", candidateKey.String(), service.Name, service.ListenPort, claim.Kind, claim.Key.String(), claim.Name)
		}
		if claims := ingressClaims[service.ListenPort]; len(claims) > 0 {
			claim := claims[0]
			return fmt.Errorf("service %s/%s listenPort %d conflicts with ingress %s/%s", candidateKey.String(), service.Name, service.ListenPort, claim.Key.String(), claim.Ingress)
		}
	}
	candidateIngress := []ingressPortClaim{}
	for _, ingress := range candidate.Spec.Ingress {
		if claim, ok := servicePorts[ingress.ListenPort]; ok {
			return fmt.Errorf("ingress %s/%s listenPort %d conflicts with service %s/%s", candidateKey.String(), ingress.Name, ingress.ListenPort, claim.Key.String(), claim.Name)
		}
		for _, route := range ingress.Routes {
			claim := ingressPortClaim{
				Key:     candidateKey,
				Ingress: ingress.Name,
				Port:    ingress.ListenPort,
				Host:    ingress.Host,
				Path:    route.Path,
				Service: route.ServiceName,
			}
			for _, existingClaim := range ingressClaims[ingress.ListenPort] {
				if ingressRouteConflicts(claim, existingClaim) {
					return fmt.Errorf("ingress %s/%s listenPort %d host %q path %q conflicts with ingress %s/%s", candidateKey.String(), ingress.Name, ingress.ListenPort, normalizeIngressHost(claim.Host), cleanIngressPath(claim.Path), existingClaim.Key.String(), existingClaim.Ingress)
				}
			}
			for _, existingClaim := range candidateIngress {
				if ingressRouteConflicts(claim, existingClaim) {
					return fmt.Errorf("ingress %s/%s listenPort %d host %q path %q conflicts with ingress %s/%s in the same runtime", candidateKey.String(), ingress.Name, ingress.ListenPort, normalizeIngressHost(claim.Host), cleanIngressPath(claim.Path), existingClaim.Key.String(), existingClaim.Ingress)
				}
			}
			candidateIngress = append(candidateIngress, claim)
		}
	}
	return nil
}

type portClaim struct {
	Key  RuntimeKey
	Kind string
	Name string
	Port int
}

type ingressPortClaim struct {
	Key     RuntimeKey
	Ingress string
	Port    int
	Host    string
	Path    string
	Service string
}

func ingressRouteConflicts(a, b ingressPortClaim) bool {
	return a.Port == b.Port && ingressHostsOverlap(a.Host, b.Host) && cleanIngressPath(a.Path) == cleanIngressPath(b.Path)
}

func ingressHostsOverlap(a, b string) bool {
	a = normalizeIngressHost(a)
	b = normalizeIngressHost(b)
	return a == "*" || b == "*" || strings.EqualFold(a, b)
}

func normalizeIngressHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "*"
	}
	return host
}

func validateNodeCapacity(candidate Runtime, existing []Runtime, node NodeConfig) error {
	runtimes := append([]Runtime{}, existing...)
	runtimes = append(runtimes, candidate)
	snapshot := resourceSnapshotFor(node, runtimes)
	if snapshot.Allocatable.MilliCPUs > 0 && snapshot.Requested.MilliCPUs > snapshot.Allocatable.MilliCPUs {
		return fmt.Errorf("node %s CPU capacity exceeded: requested %dm, allocatable %dm", node.Name, snapshot.Requested.MilliCPUs, snapshot.Allocatable.MilliCPUs)
	}
	if snapshot.Allocatable.MemoryBytes > 0 && snapshot.Requested.MemoryBytes > snapshot.Allocatable.MemoryBytes {
		return fmt.Errorf("node %s memory capacity exceeded: requested %d bytes, allocatable %d bytes", node.Name, snapshot.Requested.MemoryBytes, snapshot.Allocatable.MemoryBytes)
	}
	if snapshot.Allocatable.PIDs > 0 && snapshot.Requested.PIDs > snapshot.Allocatable.PIDs {
		return fmt.Errorf("node %s pids capacity exceeded: requested %d, allocatable %d", node.Name, snapshot.Requested.PIDs, snapshot.Allocatable.PIDs)
	}
	return nil
}

func resourceSnapshotFor(node NodeConfig, runtimes []Runtime) ResourceSnapshot {
	allocatable := nodeResourceRequest(node.Allocatable)
	if allocatable.Empty() {
		allocatable = nodeResourceRequest(node.Capacity)
	}
	requested := ResourceRequest{}
	for _, runtime := range runtimes {
		requested = requested.Add(runtimeResourceRequest(runtime))
	}
	return ResourceSnapshot{
		NodeName:    node.Name,
		Runtimes:    len(runtimes),
		Allocatable: allocatable,
		Requested:   requested,
		Available:   allocatable.Sub(requested),
	}
}

func nodeResourceRequest(resources ResourceSpec) ResourceRequest {
	cpus, _ := parseCPUMilli(resources.CPUs)
	memory, _ := parseMemoryBytes(resources.Memory)
	return ResourceRequest{
		MilliCPUs:   cpus,
		MemoryBytes: memory,
		PIDs:        int64(resources.PIDsLimit),
	}
}

func runtimeResourceRequest(runtime Runtime) ResourceRequest {
	runtime = NormalizeRuntime(runtime)
	request := ResourceRequest{}
	for _, deployment := range runtime.Spec.Deployments {
		replicas := int64(deploymentReplicas(deployment))
		cpus, _ := parseCPUMilli(deployment.Resources.CPUs)
		memory, _ := parseMemoryBytes(deployment.Resources.Memory)
		request = request.Add(ResourceRequest{
			MilliCPUs:   cpus * replicas,
			MemoryBytes: memory * replicas,
			PIDs:        int64(deployment.Resources.PIDsLimit) * replicas,
		})
	}
	return request
}

func (r ResourceRequest) Add(other ResourceRequest) ResourceRequest {
	return ResourceRequest{
		MilliCPUs:   r.MilliCPUs + other.MilliCPUs,
		MemoryBytes: r.MemoryBytes + other.MemoryBytes,
		PIDs:        r.PIDs + other.PIDs,
	}
}

func (r ResourceRequest) Sub(other ResourceRequest) ResourceRequest {
	return ResourceRequest{
		MilliCPUs:   maxInt64(0, r.MilliCPUs-other.MilliCPUs),
		MemoryBytes: maxInt64(0, r.MemoryBytes-other.MemoryBytes),
		PIDs:        maxInt64(0, r.PIDs-other.PIDs),
	}
}

func (r ResourceRequest) Empty() bool {
	return r.MilliCPUs == 0 && r.MemoryBytes == 0 && r.PIDs == 0
}

func parseCPUMilli(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	whole, frac, found := strings.Cut(value, ".")
	wholeValue, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	if wholeValue > math.MaxInt64/1000 {
		return 0, fmt.Errorf("cpu value %q is too large", value)
	}
	milli := wholeValue * 1000
	if !found {
		return milli, nil
	}
	if len(frac) > 3 {
		roundUp := strings.Trim(frac[3:], "0") != ""
		frac = frac[:3]
		if roundUp {
			milli++
		}
	}
	for len(frac) < 3 {
		frac += "0"
	}
	if frac != "" {
		fraction, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, err
		}
		milli += fraction
	}
	return milli, nil
}

func parseMemoryBytes(value string) (int64, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return 0, nil
	}
	index := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	if index == 0 {
		return 0, fmt.Errorf("memory value %q is invalid", value)
	}
	number, err := strconv.ParseInt(value[:index], 10, 64)
	if err != nil {
		return 0, err
	}
	multiplier := memoryMultiplier(value[index:])
	if multiplier <= 0 {
		return 0, fmt.Errorf("memory suffix %q is invalid", value[index:])
	}
	if number > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("memory value %q is too large", value)
	}
	return number * multiplier, nil
}

func memoryMultiplier(suffix string) int64 {
	switch suffix {
	case "", "b":
		return 1
	case "k", "kb":
		return 1000
	case "m", "mb":
		return 1000 * 1000
	case "g", "gb":
		return 1000 * 1000 * 1000
	case "t", "tb":
		return 1000 * 1000 * 1000 * 1000
	case "ki", "kib":
		return 1024
	case "mi", "mib":
		return 1024 * 1024
	case "gi", "gib":
		return 1024 * 1024 * 1024
	case "ti", "tib":
		return 1024 * 1024 * 1024 * 1024
	default:
		return 0
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
