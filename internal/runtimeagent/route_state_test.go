package runtimeagent

import (
	"net/http"
	"testing"
)

func TestReplaceRoutesAndStateStartsNewPortsAndStopsUnusedPorts(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner()})
	oldRuntime := testRuntime(18080, 19000)
	oldKey := KeyForRuntime(oldRuntime)
	manager.mu.Lock()
	manager.specs[oldKey.String()] = oldRuntime
	manager.routes[19000] = []proxyRoute{{Key: oldKey, Service: "api"}}
	manager.servers[19000] = &http.Server{}
	manager.mu.Unlock()

	newRuntime := testRuntime(18081, 19001)
	refreshed := map[string][]Endpoint{"api": {{Container: "api-1", Address: "127.0.0.1:9000"}}}
	starts, stops := manager.replaceRoutesAndState(newRuntime, refreshed, routesForRuntime(newRuntime))

	if !hasInt(starts, 18081) || !hasInt(starts, 19001) {
		t.Fatalf("expected new service and ingress ports to start, got %v", starts)
	}
	if !hasInt(stops, 19000) {
		t.Fatalf("expected old service port to stop, got %v", stops)
	}
}

func hasInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
