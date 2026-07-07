package runtimeagent

import (
	"strings"
)

func (m *Manager) replaceRoutesAndState(runtime Runtime, refreshed map[string][]Endpoint, routes map[int][]proxyRoute) ([]int, []int) {
	key := KeyForRuntime(runtime)
	portsToStart := []int{}
	m.mu.Lock()
	m.specs[key.String()] = runtime
	for endpointKey := range m.endpoints {
		if strings.HasPrefix(endpointKey, key.String()+"/") {
			delete(m.endpoints, endpointKey)
		}
	}
	for service, endpoints := range refreshed {
		m.endpoints[endpointKey(key, service)] = endpoints
	}
	portsToStop := m.removeRoutesForKeyLocked(key)
	for port, newRoutes := range routes {
		m.routes[port] = append(m.routes[port], newRoutes...)
		sortRoutes(m.routes[port])
		if _, ok := m.servers[port]; !ok {
			portsToStart = append(portsToStart, port)
		}
	}
	filteredStops := portsToStop[:0]
	for _, port := range portsToStop {
		if len(m.routes[port]) == 0 {
			filteredStops = append(filteredStops, port)
		}
	}
	portsToStop = filteredStops
	m.mu.Unlock()
	return portsToStart, portsToStop
}

func (m *Manager) removeRoutesForKeyLocked(key RuntimeKey) []int {
	portsToStop := []int{}
	for port, routes := range m.routes {
		filtered := routes[:0]
		for _, route := range routes {
			if route.Key != key {
				filtered = append(filtered, route)
			}
		}
		if len(filtered) == 0 {
			delete(m.routes, port)
			if _, ok := m.servers[port]; ok {
				portsToStop = append(portsToStop, port)
			}
			continue
		}
		m.routes[port] = filtered
	}
	return portsToStop
}
