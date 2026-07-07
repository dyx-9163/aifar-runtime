package runtimeagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadRuntimeConfigAppliesDefaultsAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
api:
  listen: 0.0.0.0:19081
  shutdownTimeout: 15s
docker:
  command: podman
container:
  readyTimeout: 10s
log:
  format: text
`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.API.Listen != "0.0.0.0:19081" {
		t.Fatalf("unexpected listen address: %s", config.API.Listen)
	}
	if config.Docker.Command != "podman" {
		t.Fatalf("unexpected docker command: %s", config.Docker.Command)
	}
	if config.Container.ReadyTimeout.Duration != 10*time.Second {
		t.Fatalf("unexpected ready timeout: %s", config.Container.ReadyTimeout.Duration)
	}
	if config.API.ShutdownTimeout.Duration != 15*time.Second {
		t.Fatalf("unexpected shutdown timeout: %s", config.API.ShutdownTimeout.Duration)
	}
	if config.Log.Format != "text" {
		t.Fatalf("unexpected log format: %s", config.Log.Format)
	}
	if config.State.Dir != DefaultStateDir {
		t.Fatalf("expected default state dir, got %s", config.State.Dir)
	}
}

func TestLoadRuntimeConfigRejectsInvalidDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("container:\n  readyTimeout: soon\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadRuntimeConfig(path); err == nil {
		t.Fatal("expected invalid duration error")
	}
}

func TestLoadRuntimeConfigRejectsInvalidListenAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("api:\n  listen: localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRuntimeConfig(path)
	if err == nil || !strings.Contains(err.Error(), "api.listen") {
		t.Fatalf("expected api.listen validation error, got %v", err)
	}
}

func TestValidateRuntimeConfigRejectsInvalidHealthTemplate(t *testing.T) {
	config := DefaultRuntimeConfig()
	config.Container.HTTPHealthCheckTemplate = "wget http://127.0.0.1/healthz"

	err := ValidateRuntimeConfig(config)
	if err == nil || !strings.Contains(err.Error(), "httpHealthCheckTemplate") {
		t.Fatalf("expected health check template validation error, got %v", err)
	}
}

func TestLoadRuntimeConfigReadsBearerTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("security:\n  bearerTokenFile: "+tokenPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadRuntimeConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Security.BearerToken != "secret-token" {
		t.Fatalf("unexpected bearer token: %q", config.Security.BearerToken)
	}
}

func TestLoadRuntimeConfigReadsRBACTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "viewer-token")
	if err := os.WriteFile(tokenPath, []byte("view-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
security:
  rbac:
    enabled: true
    tokens:
      - name: viewer
        role: viewer
        tokenFile: `+tokenPath+`
`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadRuntimeConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Security.RBAC.Tokens[0].Token; got != "view-secret" {
		t.Fatalf("unexpected RBAC token: %q", got)
	}
}

func TestValidateRuntimeConfigRejectsPartialTLSConfig(t *testing.T) {
	config := DefaultRuntimeConfig()
	config.Security.TLSCertFile = "/etc/aifar-runtime/tls.crt"

	err := ValidateRuntimeConfig(config)
	if err == nil || !strings.Contains(err.Error(), "tlsCertFile") {
		t.Fatalf("expected partial TLS validation error, got %v", err)
	}
}

func TestValidateRuntimeConfigReservesEtcdBackend(t *testing.T) {
	config := DefaultRuntimeConfig()
	config.State.Backend = "etcd"
	config.State.Etcd.Endpoints = []string{"127.0.0.1:2379"}

	err := ValidateRuntimeConfig(config)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved etcd backend error, got %v", err)
	}
}
