package runtimeagent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Reconciler struct {
	Manager *Manager
	Log     io.Writer
}

func (r Reconciler) ReconcileRuntime(ctx context.Context, runtime Runtime) error {
	if r.Manager == nil {
		return errors.New("runtime manager is required")
	}
	if err := r.Manager.Apply(ctx, runtime); err != nil {
		return err
	}
	logf(r.Log, "AIFAR Runtime reconciled %s\n", KeyForRuntime(runtime).String())
	return nil
}

type ManagerOptions struct {
	StateDir string
	Runner   CommandRunner
	Log      io.Writer
}

type Manager struct {
	mu          sync.RWMutex
	reconcileMu sync.Mutex
	store       *StateStore
	runner      CommandRunner
	log         io.Writer
	specs       map[string]Runtime
	statuses    map[string]RuntimeStatus
	routes      map[int][]proxyRoute
	servers     map[int]*http.Server
	next        map[string]uint64
	endpoints   map[string][]Endpoint
}

type proxyRoute struct {
	Key        RuntimeKey `json:"key"`
	Service    string     `json:"service"`
	Ingress    string     `json:"ingress,omitempty"`
	Host       string     `json:"host,omitempty"`
	PathPrefix string     `json:"pathPrefix,omitempty"`
}

func NewManager(options ManagerOptions) *Manager {
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Manager{
		store:     NewStateStore(options.StateDir),
		runner:    runner,
		log:       options.Log,
		specs:     map[string]Runtime{},
		statuses:  map[string]RuntimeStatus{},
		routes:    map[int][]proxyRoute{},
		servers:   map[int]*http.Server{},
		next:      map[string]uint64{},
		endpoints: map[string][]Endpoint{},
	}
}

func (m *Manager) StateDir() string {
	return m.store.Root()
}

func (m *Manager) Load(ctx context.Context) error {
	runtimes, err := m.store.LoadAll()
	if err != nil {
		return err
	}
	for _, runtime := range runtimes {
		if err := m.Apply(ctx, runtime); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Apply(ctx context.Context, runtime Runtime) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	runtime = NormalizeRuntime(runtime)
	key := KeyForRuntime(runtime)
	if runtime.Metadata.Generation == 0 {
		runtime.Metadata.Generation = m.nextGeneration(key)
	}
	if err := ValidateRuntime(runtime); err != nil {
		m.store.AppendEvent(key.Namespace, key.Name, "Warning", "Rejected", err.Error())
		return err
	}
	m.store.AppendEvent(key.Namespace, key.Name, "Normal", "ApplyStarted", "Runtime reconciliation started")
	if err := m.ensureNetwork(ctx, runtime.Spec.Network); err != nil {
		m.recordFailedStatus(runtime, err)
		return err
	}
	if err := m.reconcileDeployments(ctx, runtime); err != nil {
		m.recordFailedStatus(runtime, err)
		return err
	}
	refreshed, err := m.refreshRuntimeEndpoints(ctx, runtime)
	if err != nil {
		m.recordFailedStatus(runtime, err)
		return err
	}
	routes := routesForRuntime(runtime)
	portsToStart, portsToStop := m.replaceRoutesAndState(runtime, refreshed, routes)
	sort.Ints(portsToStart)
	sort.Ints(portsToStop)
	for _, port := range portsToStart {
		if err := m.startPort(port); err != nil {
			m.recordFailedStatus(runtime, err)
			return err
		}
	}
	for _, port := range portsToStop {
		m.stopPort(ctx, port)
	}
	status := NewStatus(runtime, "Ready", refreshed, nil)
	m.mu.Lock()
	m.statuses[key.String()] = status
	m.mu.Unlock()
	if err := m.store.SaveRuntime(runtime); err != nil {
		return err
	}
	if err := m.store.SaveStatus(runtime, status); err != nil {
		return err
	}
	if err := m.writeProxyState(); err != nil {
		return err
	}
	m.store.AppendEvent(key.Namespace, key.Name, "Normal", "Applied", "Runtime reconciliation completed")
	logf(m.log, "AIFAR runtime applied %s ports=%v\n", key.String(), sortedRoutePorts(routes))
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (m *Manager) Remove(ctx context.Context, namespace, name string) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	key := runtimeKey(namespace, name)
	if key.Name == "" {
		return errors.New("runtime name is required")
	}
	m.store.AppendEvent(key.Namespace, key.Name, "Normal", "DeleteStarted", "Runtime deletion started")
	var runtime Runtime
	var hasRuntime bool
	m.mu.Lock()
	runtime, hasRuntime = m.specs[key.String()]
	delete(m.specs, key.String())
	delete(m.statuses, key.String())
	for endpointKey := range m.endpoints {
		if strings.HasPrefix(endpointKey, key.String()+"/") {
			delete(m.endpoints, endpointKey)
		}
	}
	portsToStop := m.removeRoutesForKeyLocked(key)
	m.mu.Unlock()
	for _, port := range portsToStop {
		m.stopPort(ctx, port)
	}
	if !hasRuntime {
		loaded, err := m.store.ReadRuntime(key.Namespace, key.Name)
		if err == nil {
			runtime = loaded
			hasRuntime = true
		}
	}
	if hasRuntime {
		if err := m.removeOwnedContainers(ctx, runtime); err != nil {
			return err
		}
	}
	if err := m.store.DeleteRuntime(key.Namespace, key.Name); err != nil {
		return err
	}
	if err := m.writeProxyState(); err != nil {
		return err
	}
	m.store.AppendEvent(key.Namespace, key.Name, "Normal", "Deleted", "Runtime state, listeners, and owned containers removed")
	return nil
}

func (m *Manager) Status() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	listeners := make([]int, 0, len(m.servers))
	for port := range m.servers {
		listeners = append(listeners, port)
	}
	sort.Ints(listeners)
	instances := make([]map[string]any, 0, len(m.specs))
	for _, key := range sortedRuntimeKeys(m.specs) {
		runtime := m.specs[key]
		status := m.statuses[key]
		instances = append(instances, map[string]any{
			"apiVersion": runtime.APIVersion,
			"kind":       runtime.Kind,
			"metadata":   runtime.Metadata,
			"spec":       runtime.Spec,
			"status":     status,
		})
	}
	return map[string]any{
		"status":    "running",
		"version":   RuntimeVersion,
		"stateDir":  m.store.Root(),
		"listeners": listeners,
		"runtimes":  instances,
		"features": []string{
			"runtime-v0.1",
			"rendered-runtime-yaml",
			"deployment",
			"service-listener",
			"builtin-ingress",
			"json-state-store",
			"events",
			"docker-owner-labels",
			"legacy-spec-read",
		},
	}
}

func (m *Manager) GetRuntime(namespace, name string) (Runtime, RuntimeStatus, bool) {
	key := runtimeKey(namespace, name)
	m.mu.RLock()
	runtime, ok := m.specs[key.String()]
	status := m.statuses[key.String()]
	m.mu.RUnlock()
	if ok {
		return runtime, status, true
	}
	runtime, err := m.store.ReadRuntime(key.Namespace, key.Name)
	if err != nil {
		return Runtime{}, RuntimeStatus{}, false
	}
	status, _ = m.store.ReadStatus(key.Namespace, key.Name)
	return runtime, status, true
}

func (m *Manager) Events(namespace, name string, tail int) ([]RuntimeEvent, error) {
	return m.store.ReadEvents(namespace, name, tail)
}

func (m *Manager) Ready(ctx context.Context) error {
	if err := m.store.Ensure(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := m.runner.Run(ctx, "docker", "info")
	return err
}

func (m *Manager) Resync(ctx context.Context) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	for _, runtime := range m.snapshotRuntimes() {
		if err := m.reconcileDeployments(ctx, runtime); err != nil {
			return err
		}
		refreshed, err := m.refreshRuntimeEndpoints(ctx, runtime)
		if err != nil {
			return err
		}
		key := KeyForRuntime(runtime)
		m.mu.Lock()
		for service, endpoints := range refreshed {
			m.endpoints[endpointKey(key, service)] = endpoints
		}
		m.statuses[key.String()] = NewStatus(runtime, "Ready", refreshed, nil)
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) StartRuntimeResync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Resync(ctx); err != nil {
				logf(m.log, "AIFAR runtime periodic resync failed: %v\n", err)
			}
		}
	}
}

func (m *Manager) StartDockerEventSync(ctx context.Context, debounce time.Duration) {
	if debounce <= 0 {
		debounce = 2 * time.Second
	}
	for ctx.Err() == nil {
		if err := m.watchDockerEvents(ctx, debounce); err != nil && ctx.Err() == nil {
			logf(m.log, "AIFAR Docker event watcher stopped: %v\n", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (m *Manager) watchDockerEvents(ctx context.Context, debounce time.Duration) error {
	cmd := exec.CommandContext(ctx, "docker", "events",
		"--filter", "type=container",
		"--filter", "label=aifar.runtime/managed=true",
		"--format", "{{.TimeNano}} {{.Action}} {{.Actor.Attributes.aifar.runtime/name}}",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		data, _ := io.ReadAll(stderr)
		if len(strings.TrimSpace(string(data))) > 0 {
			logf(m.log, "AIFAR Docker events stderr: %s\n", strings.TrimSpace(string(data)))
		}
	}()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			_ = cmd.Wait()
			return ctx.Err()
		case <-time.After(debounce):
		}
		if err := m.Resync(ctx); err != nil {
			logf(m.log, "AIFAR runtime Docker event resync failed: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

func (m *Manager) nextGeneration(key RuntimeKey) int64 {
	m.mu.RLock()
	existing, ok := m.specs[key.String()]
	m.mu.RUnlock()
	if ok && existing.Metadata.Generation > 0 {
		return existing.Metadata.Generation + 1
	}
	return 1
}

func (m *Manager) recordFailedStatus(runtime Runtime, err error) {
	key := KeyForRuntime(runtime)
	status := NewStatus(runtime, "Failed", nil, err)
	m.mu.Lock()
	m.specs[key.String()] = runtime
	m.statuses[key.String()] = status
	m.mu.Unlock()
	_ = m.store.SaveRuntime(runtime)
	_ = m.store.SaveStatus(runtime, status)
	m.store.AppendEvent(key.Namespace, key.Name, "Warning", "ReconcileFailed", err.Error())
}

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

func (m *Manager) ensureNetwork(ctx context.Context, network string) error {
	network = strings.TrimSpace(network)
	if network == "" {
		return errors.New("docker network is required")
	}
	if _, err := m.runner.Run(ctx, "docker", "network", "inspect", network); err == nil {
		return nil
	}
	if _, err := m.runner.Run(ctx, "docker", "network", "create", network); err != nil {
		return fmt.Errorf("ensure docker network %s: %w", network, err)
	}
	return nil
}

func (m *Manager) reconcileDeployments(ctx context.Context, runtime Runtime) error {
	for _, deployment := range runtime.Spec.Deployments {
		if err := m.ensureDeployment(ctx, runtime, deployment); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ensureDeployment(ctx context.Context, runtime Runtime, deployment DeploymentSpec) error {
	replicas := deploymentReplicas(deployment)
	for replica := 1; replica <= replicas; replica++ {
		name := containerNameForDeployment(runtime, deployment, replica)
		exists, err := m.containerExists(ctx, name)
		if err != nil {
			return err
		}
		if exists {
			recreate, err := m.containerNeedsRecreate(ctx, name, deployment)
			if err != nil {
				return err
			}
			if recreate {
				if _, err := m.runner.Run(ctx, "docker", "rm", "-f", name); err != nil {
					return fmt.Errorf("replace drifted AIFAR pod %s: %w", name, err)
				}
				exists = false
			}
		}
		if !exists {
			if err := m.runContainer(ctx, runtime, deployment, replica, name); err != nil {
				return err
			}
		}
	}
	return m.removeExtraReplicas(ctx, runtime, deployment)
}

func (m *Manager) containerExists(ctx context.Context, name string) (bool, error) {
	_, err := m.runner.Run(ctx, "docker", "inspect", "-f", "{{.Id}}", name)
	if err == nil {
		return true, nil
	}
	return false, nil
}

func (m *Manager) containerNeedsRecreate(ctx context.Context, name string, deployment DeploymentSpec) (bool, error) {
	result, err := m.runner.Run(ctx, "docker", "inspect", "-f", `{{index .Config.Labels "aifar.runtime/spec-hash"}}`, name)
	if err != nil || strings.TrimSpace(result.Stdout) == "" {
		result, err = m.runner.Run(ctx, "docker", "inspect", "-f", `{{index .Config.Labels "aifar.spec-hash"}}`, name)
	}
	if err != nil {
		return false, nil
	}
	current := strings.TrimSpace(result.Stdout)
	return current == "" || current != deploymentSpecHash(deployment), nil
}

func (m *Manager) runContainer(ctx context.Context, runtime Runtime, deployment DeploymentSpec, replica int, name string) error {
	key := KeyForRuntime(runtime)
	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped"}
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
		"--add-host", "host.docker.internal:host-gateway",
	)
	for key, value := range deployment.Labels {
		if strings.TrimSpace(key) != "" {
			args = append(args, "--label", key+"="+value)
		}
	}
	if deployment.Resources.CPUs != "" {
		args = append(args, "--cpus", deployment.Resources.CPUs)
	}
	if deployment.Resources.Memory != "" {
		args = append(args, "--memory", deployment.Resources.Memory, "--memory-swap", deployment.Resources.Memory)
	}
	if healthCommand := healthCheckCommand(deployment); healthCommand != "" {
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
		args = append(args, "--entrypoint", strings.Join(deployment.Entrypoint, " "))
	}
	args = append(args, deployment.Image)
	args = append(args, deployment.Command...)
	if _, err := m.runner.Run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("start AIFAR pod %s: %w", name, err)
	}
	if err := m.waitContainerReady(ctx, name); err != nil {
		return err
	}
	logf(m.log, "AIFAR runtime pod started runtime=%s deployment=%s replica=%d container=%s\n", KeyForRuntime(runtime).String(), deployment.Name, replica, name)
	return nil
}

func (m *Manager) waitContainerReady(ctx context.Context, name string) error {
	deadline := time.Now().Add(5 * time.Minute)
	lastInspect := ""
	for {
		result, err := m.runner.Run(ctx, "docker", "inspect", "-f", `{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}`, name)
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
		case <-time.After(3 * time.Second):
		}
	}
}

func (m *Manager) containerReadyDiagnostics(ctx context.Context, name, lastInspect string) string {
	var b strings.Builder
	if strings.TrimSpace(lastInspect) != "" {
		fmt.Fprintf(&b, "last inspect: %s\n", trimDiagnosticOutput(lastInspect, 1024))
	}
	inspectFormat := `status={{.State.Status}} running={{.State.Running}} exitCode={{.State.ExitCode}} error={{.State.Error}} oomKilled={{.State.OOMKilled}}{{if .State.Health}} health={{.State.Health.Status}}{{end}}`
	if result, err := m.runner.Run(ctx, "docker", "inspect", "-f", inspectFormat, name); err != nil {
		fmt.Fprintf(&b, "inspect failed: %v", err)
		if strings.TrimSpace(result.Stderr) != "" {
			fmt.Fprintf(&b, ": %s", trimDiagnosticOutput(strings.TrimSpace(result.Stderr), 1024))
		}
		b.WriteString("\n")
	} else if strings.TrimSpace(result.Stdout) != "" {
		fmt.Fprintf(&b, "inspect: %s\n", trimDiagnosticOutput(strings.TrimSpace(result.Stdout), 2048))
	}
	healthFormat := `{{if .State.Health}}{{range .State.Health.Log}}{{println .Start "exit=" .ExitCode "output=" .Output}}{{end}}{{end}}`
	if result, err := m.runner.Run(ctx, "docker", "inspect", "-f", healthFormat, name); err == nil && strings.TrimSpace(result.Stdout) != "" {
		fmt.Fprintf(&b, "health log:\n%s\n", trimDiagnosticOutput(strings.TrimSpace(result.Stdout), 4096))
	}
	if result, err := m.runner.Run(ctx, "docker", "logs", "--tail", "120", name); err != nil {
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

func (m *Manager) removeExtraReplicas(ctx context.Context, runtime Runtime, deployment DeploymentSpec) error {
	key := KeyForRuntime(runtime)
	result, err := m.runner.Run(ctx, "docker",
		"ps", "-a",
		"--filter", "label=aifar.runtime/managed=true",
		"--filter", "label=aifar.runtime/namespace="+key.Namespace,
		"--filter", "label=aifar.runtime/name="+key.Name,
		"--filter", "label=aifar.runtime/deployment="+deployment.Name,
		"--format", `{{.Names}}|{{.Label "aifar.runtime/replica"}}|{{.Label "aifar.runtime/revision"}}`,
	)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		replica, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		revision := ""
		if len(parts) == 3 {
			revision = strings.TrimSpace(parts[2])
		}
		if replica > deploymentReplicas(deployment) || (revision != "" && revision != deployment.Revision) {
			_, _ = m.runner.Run(ctx, "docker", "rm", "-f", name)
			logf(m.log, "AIFAR runtime pod removed deployment=%s replica=%d container=%s\n", deployment.Name, replica, name)
		}
	}
	return nil
}

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
		Name        string            `json:"name"`
		Image       string            `json:"image,omitempty"`
		Selector    map[string]string `json:"selector,omitempty"`
		Ports       []ContainerPort   `json:"ports,omitempty"`
		Env         map[string]string `json:"env,omitempty"`
		EnvFiles    []string          `json:"envFiles,omitempty"`
		EnvFrom     []EnvFromSource   `json:"envFrom,omitempty"`
		Volumes     []VolumeMount     `json:"volumes,omitempty"`
		Resources   ResourceSpec      `json:"resources,omitempty"`
		HealthCheck HealthCheckSpec   `json:"healthCheck,omitempty"`
		Entrypoint  []string          `json:"entrypoint,omitempty"`
		Command     []string          `json:"command,omitempty"`
		Labels      map[string]string `json:"labels,omitempty"`
		Revision    string            `json:"revision,omitempty"`
	}
	data, _ := json.Marshal(hashDeployment{
		Name:        deployment.Name,
		Image:       deployment.Image,
		Selector:    deployment.Selector,
		Ports:       deployment.Ports,
		Env:         deployment.Env,
		EnvFiles:    deployment.EnvFiles,
		EnvFrom:     deployment.EnvFrom,
		Volumes:     deployment.Volumes,
		Resources:   deployment.Resources,
		HealthCheck: deployment.HealthCheck,
		Entrypoint:  deployment.Entrypoint,
		Command:     deployment.Command,
		Labels:      deployment.Labels,
		Revision:    deployment.Revision,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func healthCheckCommand(deployment DeploymentSpec) string {
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
	return fmt.Sprintf("wget -qO- http://127.0.0.1:%d%s >/dev/null", port, path)
}

func (m *Manager) removeOwnedContainers(ctx context.Context, runtime Runtime) error {
	key := KeyForRuntime(runtime)
	result, err := m.runner.Run(ctx, "docker",
		"ps", "-a",
		"--filter", "label=aifar.runtime/managed=true",
		"--filter", "label=aifar.runtime/namespace="+key.Namespace,
		"--filter", "label=aifar.runtime/name="+key.Name,
		"--format", "{{.Names}}",
	)
	if err != nil {
		return nil
	}
	for _, name := range strings.Fields(result.Stdout) {
		if _, err := m.runner.Run(ctx, "docker", "rm", "-f", name); err != nil {
			return fmt.Errorf("remove owned container %s: %w", name, err)
		}
	}
	return nil
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

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}
