package runtimeagent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestManagerApplyIsIdempotentAndWritesState(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	runtime := testRuntime(ingressPort, servicePort)

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if got := runner.countCalls("docker run "); got != 1 {
		t.Fatalf("expected idempotent apply to run one container, got %d calls:\n%s", got, strings.Join(runner.snapshotCalls(), "\n"))
	}
	if _, err := os.Stat(filepath.Join(manager.StateDir(), "specs", "prod", "demo.json")); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	features := strings.Join(anyStrings(status["features"]), ",")
	if strings.Contains(strings.ToLower(features), "nacos") {
		t.Fatalf("status must not expose Nacos features, got %s", features)
	}
	if err := manager.Remove(context.Background(), "prod", "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerApplyRecordsFailedSubresourceStatus(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: failingDockerRunner{
		failNeedle: "docker network",
		err:        errors.New("docker is unavailable"),
	}})
	runtime := testRuntime(ingressPort, servicePort)

	err := manager.Apply(context.Background(), runtime)
	if err == nil {
		t.Fatal("expected apply to fail")
	}
	_, status, found := manager.GetRuntime("prod", "demo")
	if !found {
		t.Fatal("runtime status not found")
	}
	if status.Phase != "Failed" {
		t.Fatalf("expected failed phase, got %#v", status)
	}
	if len(status.Deployments) != 1 || status.Deployments[0].Phase != "Failed" {
		t.Fatalf("expected failed deployment status, got %#v", status.Deployments)
	}
	if len(status.Services) != 1 || status.Services[0].Phase != "Failed" {
		t.Fatalf("expected failed service status, got %#v", status.Services)
	}
	if len(status.Ingress) != 1 || status.Ingress[0].Phase != "Failed" {
		t.Fatalf("expected failed ingress status, got %#v", status.Ingress)
	}
}

func testRuntime(ingressPort, servicePort int) Runtime {
	return NormalizeRuntime(Runtime{
		APIVersion: DefaultAPIVersion,
		Kind:       DefaultKind,
		Metadata: ObjectMeta{
			Name:      "demo",
			Namespace: "prod",
		},
		Spec: RuntimeSpec{
			Deployments: []DeploymentSpec{{
				Name:     "api",
				Image:    "demo-api:1",
				Selector: map[string]string{"app": "api"},
				Ports:    []ContainerPort{{Name: "http", ContainerPort: 9000}},
				Revision: "rev-1",
			}},
			Services: []ServiceSpec{{
				Name:       "api",
				Selector:   map[string]string{"app": "api"},
				Port:       9000,
				TargetPort: FromString("http"),
				ListenPort: servicePort,
			}},
			Ingress: []IngressSpec{{
				Name:       "public",
				Provider:   "builtin",
				ListenPort: ingressPort,
				Routes:     []IngressRoute{{Path: "/api", ServiceName: "api", ServicePort: FromInt(9000)}},
			}},
		},
	})
}

type fakeDockerRunner struct {
	mu         sync.Mutex
	calls      []string
	containers map[string]fakeContainer
}

type fakeContainer struct {
	labels map[string]string
	ip     string
}

func newFakeDockerRunner() *fakeDockerRunner {
	return &fakeDockerRunner{containers: map[string]fakeContainer{}}
}

func (r *fakeDockerRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if name != "docker" || len(args) == 0 {
		return CommandResult{Stdout: "ok\n"}, nil
	}
	switch args[0] {
	case "info", "network":
		return CommandResult{Stdout: "ok\n"}, nil
	case "inspect":
		return r.inspect(args)
	case "run":
		return r.run(args), nil
	case "ps":
		return r.ps(args), nil
	case "rm":
		if len(args) >= 3 && args[1] == "-f" {
			delete(r.containers, args[2])
		}
		return CommandResult{Stdout: "removed\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *fakeDockerRunner) inspect(args []string) (CommandResult, error) {
	if len(args) < 4 {
		return CommandResult{}, errors.New("invalid inspect")
	}
	format := args[2]
	name := args[3]
	container, ok := r.containers[name]
	if !ok {
		return CommandResult{}, errors.New("not found")
	}
	switch {
	case format == "{{.Id}}":
		return CommandResult{Stdout: "container-id\n"}, nil
	case strings.Contains(format, "aifar.runtime/spec-hash"):
		return CommandResult{Stdout: container.labels["aifar.runtime/spec-hash"] + "\n"}, nil
	case strings.Contains(format, "aifar.spec-hash"):
		return CommandResult{Stdout: container.labels["aifar.spec-hash"] + "\n"}, nil
	case strings.Contains(format, "NetworkSettings"):
		ip := container.ip
		if ip == "" {
			ip = "172.20.0.10"
		}
		return CommandResult{Stdout: "true|healthy|" + ip + "\n"}, nil
	case strings.Contains(format, ".State.Running"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *fakeDockerRunner) run(args []string) CommandResult {
	name := ""
	labels := map[string]string{}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case "--label":
			if i+1 < len(args) {
				key, value, _ := strings.Cut(args[i+1], "=")
				labels[key] = value
				i++
			}
		}
	}
	if name != "" {
		r.containers[name] = fakeContainer{labels: labels, ip: "172.20.0." + strconv.Itoa(10+len(r.containers))}
	}
	return CommandResult{Stdout: name + "\n"}
}

func (r *fakeDockerRunner) ps(args []string) CommandResult {
	filters := labelFilters(args)
	format := dockerFormat(args)
	lines := []string{}
	for name, container := range r.containers {
		if !labelsInclude(container.labels, filters) {
			continue
		}
		if strings.Contains(format, `aifar.runtime/replica`) {
			lines = append(lines, strings.Join([]string{
				name,
				container.labels["aifar.runtime/replica"],
				container.labels["aifar.runtime/revision"],
			}, "|"))
			continue
		}
		lines = append(lines, name)
	}
	sortStrings(lines)
	return CommandResult{Stdout: strings.Join(lines, "\n")}
}

func (r *fakeDockerRunner) snapshotCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *fakeDockerRunner) countCalls(needle string) int {
	count := 0
	for _, call := range r.snapshotCalls() {
		if strings.Contains(call, needle) {
			count++
		}
	}
	return count
}

func (r *fakeDockerRunner) firstCallContaining(needle string) string {
	for _, call := range r.snapshotCalls() {
		if strings.Contains(call, needle) {
			return call
		}
	}
	return ""
}

func (r *fakeDockerRunner) addContainer(name string, labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := map[string]string{}
	for key, value := range labels {
		copied[key] = value
	}
	r.containers[name] = fakeContainer{labels: copied, ip: "172.20.0.99"}
}

func (r *fakeDockerRunner) hasContainer(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.containers[name]
	return ok
}

func (r *fakeDockerRunner) countOwned(namespace, name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, container := range r.containers {
		if container.labels["aifar.runtime/namespace"] == namespace && container.labels["aifar.runtime/name"] == name {
			count++
		}
	}
	return count
}

func labelFilters(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		if args[i] != "--filter" || i+1 >= len(args) {
			continue
		}
		value := args[i+1]
		i++
		value = strings.TrimPrefix(value, "label=")
		key, val, ok := strings.Cut(value, "=")
		if ok {
			out[key] = val
		}
	}
	return out
}

func dockerFormat(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--format" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func labelsInclude(labels map[string]string, filters map[string]string) bool {
	for key, value := range filters {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func anyStrings(value any) []string {
	items, _ := value.([]string)
	return items
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

type failingDockerRunner struct {
	failNeedle string
	err        error
}

func (r failingDockerRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	if strings.Contains(call, r.failNeedle) {
		return CommandResult{Stderr: r.err.Error()}, r.err
	}
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		return CommandResult{}, errors.New("not found")
	case strings.Contains(call, "docker inspect -f {{.State.Running}}"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker ps --filter") || strings.Contains(call, "docker ps -a"):
		return CommandResult{}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
