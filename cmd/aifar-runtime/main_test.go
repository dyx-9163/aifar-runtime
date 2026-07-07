package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"aifar-runtime/internal/runtimeagent"
)

func TestStatusDoesNotRequireDockerHealth(t *testing.T) {
	handler := newRuntimeHandler(runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}), func(context.Context) error {
		return errors.New("docker is not ready")
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"running"`) {
		t.Fatalf("unexpected status response: %s", recorder.Body.String())
	}
}

func TestHealthStillReportsDockerHealth(t *testing.T) {
	handler := newRuntimeHandler(runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}), func(context.Context) error {
		return errors.New("docker is not ready")
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHealthzDoesNotRequireDockerReadiness(t *testing.T) {
	handler := newRuntimeHandler(runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}), func(context.Context) error {
		return errors.New("docker is not ready")
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthz code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestRuntimeHandlerRequiresBearerTokenWhenConfigured(t *testing.T) {
	handler := newRuntimeHandlerWithOptions(
		runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}),
		func(context.Context) error { return nil },
		runtimeHandlerOptions{AuthToken: "secret", MetricsEnabled: true, Build: currentBuildInfo()},
	)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status without token code = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthz should stay public, code = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status with token code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestRuntimeHandlerServesMetricsAndVersion(t *testing.T) {
	handler := newRuntimeHandlerWithOptions(
		runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}),
		func(context.Context) error { return nil },
		runtimeHandlerOptions{MetricsEnabled: true, Build: buildInfo{Version: "test", Commit: "abc", BuildDate: "now", RuntimeVersion: runtimeagent.RuntimeVersion}},
	)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "aifar_runtime_info") {
		t.Fatalf("metrics code = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/version", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"version":"test"`) {
		t.Fatalf("version code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIBaseURLAcceptsExplicitScheme(t *testing.T) {
	if got := apiBaseURL("https://runtime.local:18443/"); got != "https://runtime.local:18443" {
		t.Fatalf("unexpected base URL: %s", got)
	}
	if got := apiBaseURL("127.0.0.1:18081"); got != "http://127.0.0.1:18081" {
		t.Fatalf("unexpected default base URL: %s", got)
	}
}

func TestRuntimeAPIApplyStatusEventsDeleteLoop(t *testing.T) {
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir(), Runner: emptyDockerRunner{}})
	handler := newRuntimeHandler(manager, manager.Ready)
	servicePort := freeHTTPPort(t)
	ingressPort := freeHTTPPort(t)
	body := []byte(`
apiVersion: aifar.io/v1
kind: Runtime
metadata:
  name: demo
  namespace: prod
spec:
  deployments:
    - name: api
      image: demo-api:1
      replicas: 0
      ports:
        - name: http
          containerPort: 9000
  services:
    - name: api
      selector:
        app: api
      port: 9000
      targetPort: http
      listenPort: ` + strconv.Itoa(servicePort) + `
  ingress:
    - name: public
      listenPort: ` + strconv.Itoa(ingressPort) + `
      routes:
        - path: /api
          serviceName: api
          servicePort: 9000
`)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/apis/aifar.io/v1/namespaces/prod/runtimes/demo", bytes.NewReader(body))
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/apis/aifar.io/v1/namespaces/prod/runtimes/demo/status", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"phase":"Ready"`) {
		t.Fatalf("status code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/apis/aifar.io/v1/namespaces/prod/runtimes/demo/events?tail=10", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"Applied"`) {
		t.Fatalf("events code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/apis/aifar.io/v1/namespaces/prod/runtimes/demo", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestNacosCommandsReturnUnsupported(t *testing.T) {
	err := run("register-nacos", nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported Nacos command, got %v", err)
	}
}

type emptyDockerRunner struct{}

func (emptyDockerRunner) Run(ctx context.Context, name string, args ...string) (runtimeagent.CommandResult, error) {
	return runtimeagent.CommandResult{}, nil
}

func freeHTTPPort(t *testing.T) int {
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
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
