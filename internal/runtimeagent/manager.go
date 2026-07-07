package runtimeagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
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
	StateDir     string
	Config       RuntimeConfig
	Store        RuntimeStateStore
	Runner       CommandRunner
	Scheduler    RuntimeScheduler
	Log          io.Writer
	DockerEvents DockerEventWatcher
}

type DockerEventWatcher func(ctx context.Context) (stdout io.ReadCloser, stderr io.ReadCloser, wait func() error, err error)

type Manager struct {
	mu           sync.RWMutex
	reconcileMu  sync.Mutex
	store        RuntimeStateStore
	runner       CommandRunner
	log          io.Writer
	config       RuntimeConfig
	dockerEvents DockerEventWatcher
	scheduler    RuntimeScheduler
	specs        map[string]Runtime
	statuses     map[string]RuntimeStatus
	routes       map[int][]proxyRoute
	servers      map[int]*http.Server
	next         map[string]uint64
	endpoints    map[string][]Endpoint
	restarts     map[string]restartRecord
}

func NewManager(options ManagerOptions) *Manager {
	config := NormalizeRuntimeConfig(options.Config)
	if strings.TrimSpace(options.StateDir) != "" {
		config.State.Dir = strings.TrimSpace(options.StateDir)
	}
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	store := options.Store
	if store == nil {
		store = NewStateStore(config.State.Dir)
	}
	dockerEvents := options.DockerEvents
	if dockerEvents == nil {
		dockerEvents = func(ctx context.Context) (io.ReadCloser, io.ReadCloser, func() error, error) {
			return defaultDockerEventWatcher(ctx, config.Docker.Command)
		}
	}
	manager := &Manager{
		store:        store,
		runner:       runner,
		log:          options.Log,
		config:       config,
		dockerEvents: dockerEvents,
		specs:        map[string]Runtime{},
		statuses:     map[string]RuntimeStatus{},
		routes:       map[int][]proxyRoute{},
		servers:      map[int]*http.Server{},
		next:         map[string]uint64{},
		endpoints:    map[string][]Endpoint{},
		restarts:     map[string]restartRecord{},
	}
	if options.Scheduler != nil {
		manager.scheduler = options.Scheduler
	} else {
		manager.scheduler = NewSingleNodeScheduler(manager)
	}
	return manager
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
	previousRuntime, previousStatus, hasPrevious := m.previousRuntimeState(key)
	if runtime.Metadata.Generation == 0 {
		runtime.Metadata.Generation = m.nextGeneration(key)
	}
	if err := ValidateRuntime(runtime); err != nil {
		m.appendEvent(key.Namespace, key.Name, "Warning", "Rejected", err.Error())
		return err
	}
	admitted, err := m.scheduler.AdmitRuntime(runtime)
	if err != nil {
		m.appendEvent(key.Namespace, key.Name, "Warning", "Rejected", err.Error())
		return err
	}
	runtime = admitted
	m.appendEvent(key.Namespace, key.Name, "Normal", "ApplyStarted", "Runtime reconciliation started")
	phase := RuntimePhasePending
	if m.runtimeExists(key) {
		phase = RuntimePhaseUpdating
	}
	m.updateRuntimeStatus(runtime, phase, nil, nil)
	if err := m.ensureNetwork(ctx, runtime.Spec.Network); err != nil {
		m.recordFailedStatus(runtime, err)
		return err
	}
	m.updateRuntimeStatus(runtime, RuntimePhaseStarting, nil, nil)
	if err := m.reconcileDeployments(ctx, runtime); err != nil {
		if hasPrevious {
			return m.rollbackRuntime(ctx, runtime, previousRuntime, previousStatus, err)
		}
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
	status := NewStatusWithOptions(runtime, RuntimePhaseRunning, refreshed, nil, m.statusOptions(runtime))
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
	m.appendEvent(key.Namespace, key.Name, "Normal", "Applied", "Runtime reconciliation completed")
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
	m.appendEvent(key.Namespace, key.Name, "Normal", "DeleteStarted", "Runtime deletion started")
	var runtime Runtime
	var hasRuntime bool
	m.mu.RLock()
	runtime, hasRuntime = m.specs[key.String()]
	m.mu.RUnlock()
	if !hasRuntime {
		loaded, err := m.store.ReadRuntime(key.Namespace, key.Name)
		if err == nil {
			runtime = loaded
			hasRuntime = true
		}
	}
	if hasRuntime {
		m.updateRuntimeStatus(runtime, RuntimePhaseTerminating, nil, nil)
	}
	m.mu.Lock()
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
	m.appendEvent(key.Namespace, key.Name, "Normal", "Deleted", "Runtime state, listeners, and owned containers removed")
	return nil
}

func (m *Manager) Status() map[string]any {
	node := m.nodeStatus()
	scheduler := m.ResourceSnapshot()
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
			"spec":       cloneRuntime(runtime).Spec,
			"status":     cloneRuntimeStatus(status),
		})
	}
	return map[string]any{
		"status":    "running",
		"version":   RuntimeVersion,
		"stateDir":  m.store.Root(),
		"node":      node,
		"scheduler": scheduler,
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
			"runtime-status-machine",
			"self-healing",
			"single-node-model",
			"scheduler-lite",
			"global-port-admission",
			"resource-capacity-admission",
			"rolling-update-rollback",
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
		return cloneRuntime(runtime), cloneRuntimeStatus(status), true
	}
	runtime, err := m.store.ReadRuntime(key.Namespace, key.Name)
	if err != nil {
		return Runtime{}, RuntimeStatus{}, false
	}
	status, _ = m.store.ReadStatus(key.Namespace, key.Name)
	return cloneRuntime(runtime), cloneRuntimeStatus(status), true
}

func (m *Manager) Events(namespace, name string, tail int) ([]RuntimeEvent, error) {
	return m.store.ReadEvents(namespace, name, tail)
}

func (m *Manager) Ready(ctx context.Context) error {
	if err := m.store.Ensure(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, m.config.Health.DockerTimeout.Duration)
	defer cancel()
	_, err := m.docker(ctx, "info")
	return err
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()

	m.mu.Lock()
	servers := m.servers
	m.servers = map[int]*http.Server{}
	m.routes = map[int][]proxyRoute{}
	m.mu.Unlock()

	ports := make([]int, 0, len(servers))
	for port := range servers {
		ports = append(ports, port)
	}
	sort.Ints(ports)

	var shutdownErr error
	for _, port := range ports {
		if err := servers[port].Shutdown(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown proxy port %d: %w", port, err))
		}
	}
	if err := m.writeProxyState(); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	return shutdownErr
}

func (m *Manager) docker(ctx context.Context, args ...string) (CommandResult, error) {
	return m.runner.Run(ctx, m.config.Docker.Command, args...)
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

func (m *Manager) runtimeExists(key RuntimeKey) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.specs[key.String()]
	return ok
}

func (m *Manager) updateRuntimeStatus(runtime Runtime, phase string, endpoints map[string][]Endpoint, err error) RuntimeStatus {
	key := KeyForRuntime(runtime)
	status := NewStatusWithOptions(runtime, phase, endpoints, err, m.statusOptions(runtime))
	m.mu.Lock()
	m.specs[key.String()] = cloneRuntime(runtime)
	m.statuses[key.String()] = cloneRuntimeStatus(status)
	m.mu.Unlock()
	if saveErr := m.store.SaveStatus(runtime, status); saveErr != nil {
		logf(m.log, "AIFAR runtime status write failed namespace=%s name=%s phase=%s: %v\n", key.Namespace, key.Name, status.Phase, saveErr)
	}
	return status
}

func (m *Manager) recordFailedStatus(runtime Runtime, err error) {
	key := KeyForRuntime(runtime)
	status := NewStatusWithOptions(runtime, RuntimePhaseFailed, nil, err, m.statusOptions(runtime))
	m.mu.Lock()
	m.specs[key.String()] = cloneRuntime(runtime)
	m.statuses[key.String()] = cloneRuntimeStatus(status)
	m.mu.Unlock()
	_ = m.store.SaveRuntime(runtime)
	_ = m.store.SaveStatus(runtime, status)
	m.appendEvent(key.Namespace, key.Name, "Warning", "ReconcileFailed", err.Error())
}

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}

func (m *Manager) appendEvent(namespace, name, eventType, reason, message string) {
	if err := m.store.AppendEvent(namespace, name, eventType, reason, message); err != nil {
		logf(m.log, "AIFAR runtime event write failed namespace=%s name=%s reason=%s: %v\n", namespace, name, reason, err)
	}
}
