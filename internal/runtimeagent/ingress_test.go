package runtimeagent

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestManagerRoutesIngressByLongestPathPrefix(t *testing.T) {
	servicePort := freePort(t)
	webPort := freePort(t)
	ingressPort := freePort(t)
	runner := newFakeDockerRunner()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	runtime := testRuntime(ingressPort, servicePort)
	runtime.Spec.Deployments = append(runtime.Spec.Deployments, DeploymentSpec{
		Name:     "web",
		Image:    "demo-web:1",
		Selector: map[string]string{"app": "web"},
		Ports:    []ContainerPort{{Name: "http", ContainerPort: 8080}},
	})
	runtime.Spec.Services = append(runtime.Spec.Services, ServiceSpec{
		Name:       "web",
		Selector:   map[string]string{"app": "web"},
		Port:       8080,
		TargetPort: FromString("http"),
		ListenPort: webPort,
	})
	runtime.Spec.Ingress[0].Routes = []IngressRoute{
		{Path: "/", ServiceName: "web", ServicePort: FromInt(8080)},
		{Path: "/api", ServiceName: "api", ServicePort: FromInt(9000)},
	}

	if err := manager.Apply(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	gotRuntime, service, ok := manager.resolveRoute(ingressPort, httptest.NewRequest("GET", "http://example.test/api/users", nil))
	if !ok || KeyForRuntime(gotRuntime).String() != "prod/demo" || service != "api" {
		t.Fatalf("expected /api route to api, got runtime=%s service=%s ok=%t", KeyForRuntime(gotRuntime).String(), service, ok)
	}
	_, service, ok = manager.resolveRoute(ingressPort, httptest.NewRequest("GET", "http://example.test/", nil))
	if !ok || service != "web" {
		t.Fatalf("expected / route to web, got service=%s ok=%t", service, ok)
	}
	if err := manager.Remove(context.Background(), "prod", "demo"); err != nil {
		t.Fatal(err)
	}
}
