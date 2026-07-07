package runtimeagent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestManagerDeleteOnlyRemovesOwnedContainers(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	runtime := testRuntime(ingressPort, servicePort)
	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	runner.addContainer("external", map[string]string{"owner": "someone-else"})
	if err := manager.Remove(context.Background(), "prod", "demo"); err != nil {
		t.Fatal(err)
	}
	if runner.hasContainer("external") != true {
		t.Fatal("delete removed a non-owned container")
	}
	if runner.countOwned("prod", "demo") != 0 {
		t.Fatalf("expected all owned containers removed, got %d", runner.countOwned("prod", "demo"))
	}
}

func TestManagerRemoveReturnsDockerListErrors(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner()})
	runtime := testRuntime(ingressPort, servicePort)
	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	manager.runner = failingDockerRunner{
		failNeedle: "docker ps -a",
		err:        errors.New("docker ps failed"),
	}

	err := manager.Remove(context.Background(), "prod", "demo")
	if err == nil || !strings.Contains(err.Error(), "list owned AIFAR pods") {
		t.Fatalf("expected docker ps delete error, got %v", err)
	}
}

func TestManagerRunContainerSplitsEntrypointExecutableFromArgs(t *testing.T) {
	servicePort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	runtime := testRuntime(ingressPort, servicePort)
	runtime.Spec.Deployments[0].Entrypoint = []string{"/bin/app", "--config", "/etc/app.yaml"}
	runtime.Spec.Deployments[0].Command = []string{"serve"}

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	call := runner.firstCallContaining("docker run ")
	if !strings.Contains(call, "--entrypoint /bin/app demo-api:1 --config /etc/app.yaml serve") {
		t.Fatalf("expected entrypoint executable before image and args after image, got:\n%s", call)
	}
	if strings.Contains(call, "--entrypoint /bin/app --config /etc/app.yaml") {
		t.Fatalf("entrypoint args were collapsed into --entrypoint:\n%s", call)
	}
	if err := manager.Remove(context.Background(), "prod", "demo"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerContainerReadyDiagnosticsIncludesInspectAndLogs(t *testing.T) {
	runner := &diagnosticRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	got := manager.containerReadyDiagnostics(context.Background(), "aifar-pod-prod-demo-api-current-r1", "false|unhealthy")
	for _, want := range []string{
		"last inspect: false|unhealthy",
		"inspect: status=exited",
		"health log:",
		"connection refused",
		"logs:",
		"application failed to start",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected diagnostics to contain %q, got:\n%s", want, got)
		}
	}
}

type diagnosticRunner struct{}

func (diagnosticRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "status={{.State.Status}}"):
		return CommandResult{Stdout: "status=exited running=false exitCode=1 error= oomKilled=false health=unhealthy\n"}, nil
	case strings.Contains(call, "State.Health.Log"):
		return CommandResult{Stdout: "2026-07-05 exit= 1 output= connection refused\n"}, nil
	case strings.Contains(call, "docker logs"):
		return CommandResult{Stdout: "application failed to start\n"}, nil
	default:
		return CommandResult{}, nil
	}
}
