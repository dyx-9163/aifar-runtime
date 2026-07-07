package runtimeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func (m *Manager) startPort(port int) error {
	addr := ":" + strconv.Itoa(port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen runtime proxy port %d: %w", port, err)
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(m.handleProxy(port)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	m.mu.Lock()
	if _, exists := m.servers[port]; exists {
		m.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	m.servers[port] = server
	m.mu.Unlock()
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf(m.log, "AIFAR runtime proxy port %d stopped: %v\n", port, err)
		}
	}()
	logf(m.log, "AIFAR runtime proxy listening on %s\n", addr)
	return nil
}

func (m *Manager) stopPort(ctx context.Context, port int) {
	m.mu.Lock()
	server := m.servers[port]
	delete(m.servers, port)
	m.mu.Unlock()
	if server != nil {
		_ = server.Shutdown(ctx)
	}
}

func (m *Manager) handleProxy(port int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		runtime, service, ok := m.resolveRoute(port, r)
		if !ok {
			http.Error(w, "AIFAR runtime route is not configured", http.StatusServiceUnavailable)
			return
		}
		key := KeyForRuntime(runtime)
		serviceSpec, _ := serviceByName(runtime, service)
		endpoints := m.cachedEndpoints(key, service)
		if len(endpoints) == 0 {
			http.Error(w, "AIFAR runtime service has no ready endpoints", http.StatusServiceUnavailable)
			return
		}
		ep := m.selectEndpoint(r, key, serviceSpec, endpoints)
		target := &url.URL{Scheme: "http", Host: ep.Address}
		proxy := httputil.NewSingleHostReverseProxy(target)
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = r.Host
			req.Header.Set("X-AIFAR-Upstream", ep.Container)
			req.Header.Set("X-AIFAR-Runtime", key.String())
		}
		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			http.Error(rw, proxyErr.Error(), http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, r)
	}
}

func (m *Manager) writeProxyState() error {
	if err := m.store.Ensure(); err != nil {
		return err
	}
	m.mu.RLock()
	serviceRoutes := map[int][]proxyRoute{}
	ingressRoutes := map[int][]proxyRoute{}
	for port, routes := range m.routes {
		for _, route := range routes {
			if route.Ingress == "" {
				serviceRoutes[port] = append(serviceRoutes[port], route)
			} else {
				ingressRoutes[port] = append(ingressRoutes[port], route)
			}
		}
	}
	m.mu.RUnlock()
	if err := writeJSONFile(filepath.Join(m.store.Root(), "proxy", "services.json"), serviceRoutes); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(m.store.Root(), "proxy", "ingress.json"), ingressRoutes)
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
