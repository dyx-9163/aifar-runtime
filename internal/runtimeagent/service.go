package runtimeagent

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func (m *Manager) refreshRuntimeEndpoints(ctx context.Context, runtime Runtime) (map[string][]Endpoint, error) {
	refreshed := map[string][]Endpoint{}
	for _, service := range runtime.Spec.Services {
		endpoints, err := m.discoverEndpoints(ctx, runtime, service)
		if err != nil {
			return nil, err
		}
		refreshed[service.Name] = endpoints
	}
	return refreshed, nil
}

func (m *Manager) cachedEndpoints(key RuntimeKey, service string) []Endpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.endpoints[endpointKey(key, service)]
	out := make([]Endpoint, len(items))
	copy(out, items)
	return out
}

func (m *Manager) snapshotRuntimes() []Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	runtimes := make([]Runtime, 0, len(m.specs))
	for _, key := range sortedRuntimeKeys(m.specs) {
		runtimes = append(runtimes, m.specs[key])
	}
	return runtimes
}

func endpointKey(key RuntimeKey, service string) string {
	return key.String() + "/" + service
}

func (m *Manager) discoverEndpoints(ctx context.Context, runtime Runtime, service ServiceSpec) ([]Endpoint, error) {
	deployments := matchingDeployments(runtime, service.Selector)
	targetPort, err := resolveServiceTargetPort(service, deployments)
	if err != nil {
		return nil, err
	}
	endpoints := []Endpoint{}
	for _, deployment := range deployments {
		discovered, err := m.discoverDeploymentEndpoints(ctx, runtime, deployment, targetPort)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, discovered...)
	}
	sort.Slice(endpoints, func(i, j int) bool {
		return endpoints[i].Container < endpoints[j].Container
	})
	return endpoints, nil
}

func (m *Manager) discoverDeploymentEndpoints(ctx context.Context, runtime Runtime, deployment DeploymentSpec, targetPort int) ([]Endpoint, error) {
	key := KeyForRuntime(runtime)
	result, err := m.runner.Run(ctx, "docker",
		"ps",
		"--filter", "label=aifar.runtime/managed=true",
		"--filter", "label=aifar.runtime/namespace="+key.Namespace,
		"--filter", "label=aifar.runtime/name="+key.Name,
		"--filter", "label=aifar.runtime/deployment="+deployment.Name,
		"--format", "{{.Names}}",
	)
	if err != nil || strings.TrimSpace(result.Stdout) == "" {
		result, err = m.runner.Run(ctx, "docker",
			"ps",
			"--filter", "label=aifar.app=aifar",
			"--filter", "label=aifar.component=pod",
			"--filter", "label=aifar.instance="+key.Name,
			"--filter", "label=aifar.service="+deployment.Name,
			"--format", "{{.Names}}",
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list AIFAR deployment pods: %w", err)
	}
	names := strings.Fields(result.Stdout)
	endpoints := make([]Endpoint, 0, len(names))
	format := fmt.Sprintf(`{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{else}}{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}{{end}}`, runtime.Spec.Network)
	for _, name := range names {
		inspect, err := m.runner.Run(ctx, "docker", "inspect", "-f", format, name)
		if err != nil {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(inspect.Stdout), "|", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] != "true" {
			continue
		}
		if parts[1] != "" && parts[1] != "healthy" {
			continue
		}
		ip := strings.TrimSpace(parts[2])
		if ip == "" {
			continue
		}
		endpoints = append(endpoints, Endpoint{
			Container: name,
			Address:   net.JoinHostPort(ip, strconv.Itoa(targetPort)),
		})
	}
	return endpoints, nil
}

func (m *Manager) selectEndpoint(r *http.Request, key RuntimeKey, service ServiceSpec, endpoints []Endpoint) Endpoint {
	if affinity := affinityKeyForRequest(service, r); affinity != "" {
		return endpoints[int(stableHash(affinity)%uint64(len(endpoints)))]
	}
	return m.pickEndpoint(key, service.Name, endpoints)
}

func (m *Manager) pickEndpoint(key RuntimeKey, service string, endpoints []Endpoint) Endpoint {
	nextKey := key.String() + "/" + service
	m.mu.Lock()
	index := m.next[nextKey]
	m.next[nextKey] = index + 1
	m.mu.Unlock()
	return endpoints[int(index%uint64(len(endpoints)))]
}

func affinityKeyForRequest(service ServiceSpec, r *http.Request) string {
	if r == nil {
		return ""
	}
	policy := strings.ToLower(strings.TrimSpace(service.AffinityPolicy))
	switch policy {
	case "client-ip":
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = strings.TrimSpace(r.RemoteAddr)
		}
		if host != "" {
			return "remote:" + host
		}
	case "header":
		for _, header := range []string{
			"X-AIFAR-Affinity",
			"X-Upload-Id",
			"X-File-Md5",
			"X-File-Hash",
			"X-Trace-Id",
			"X-Request-Id",
			"Authorization",
		} {
			if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
				return header + ":" + value
			}
		}
	}
	return ""
}

func stableHash(value string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum64()
}
