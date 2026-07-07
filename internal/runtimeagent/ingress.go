package runtimeagent

import (
	"net"
	"net/http"
	"sort"
	"strings"
)

type proxyRoute struct {
	Key        RuntimeKey `json:"key"`
	Service    string     `json:"service"`
	Ingress    string     `json:"ingress,omitempty"`
	Host       string     `json:"host,omitempty"`
	PathPrefix string     `json:"pathPrefix,omitempty"`
}

func (m *Manager) resolveRoute(port int, r *http.Request) (Runtime, string, bool) {
	path := "/"
	host := ""
	if r != nil {
		path = r.URL.Path
		host = hostWithoutPort(r.Host)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	routes := m.routes[port]
	for _, route := range routes {
		if route.Host != "" && route.Host != "*" && route.Host != host {
			continue
		}
		if route.PathPrefix != "" && !pathMatchesPrefix(path, route.PathPrefix) {
			continue
		}
		runtime, ok := m.specs[route.Key.String()]
		if !ok {
			continue
		}
		return runtime, route.Service, true
	}
	return Runtime{}, "", false
}

func routesForRuntime(runtime Runtime) map[int][]proxyRoute {
	runtime = NormalizeRuntime(runtime)
	key := KeyForRuntime(runtime)
	routes := map[int][]proxyRoute{}
	for _, service := range runtime.Spec.Services {
		routes[service.ListenPort] = append(routes[service.ListenPort], proxyRoute{
			Key:     key,
			Service: service.Name,
		})
	}
	for _, ingress := range runtime.Spec.Ingress {
		for _, route := range ingress.Routes {
			routes[ingress.ListenPort] = append(routes[ingress.ListenPort], proxyRoute{
				Key:        key,
				Service:    route.ServiceName,
				Ingress:    ingress.Name,
				Host:       ingress.Host,
				PathPrefix: cleanIngressPath(route.Path),
			})
		}
	}
	for port := range routes {
		sortRoutes(routes[port])
	}
	return routes
}

func sortRoutes(routes []proxyRoute) {
	sort.SliceStable(routes, func(i, j int) bool {
		if len(routes[i].PathPrefix) != len(routes[j].PathPrefix) {
			return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
		}
		return routes[i].Service < routes[j].Service
	})
}

func sortedRoutePorts(routes map[int][]proxyRoute) []int {
	ports := make([]int, 0, len(routes))
	for port := range routes {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func sortedRuntimeKeys(specs map[string]Runtime) []string {
	keys := make([]string, 0, len(specs))
	for key := range specs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pathMatchesPrefix(path, prefix string) bool {
	if prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/")
}

func hostWithoutPort(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		return parsed
	}
	return host
}
