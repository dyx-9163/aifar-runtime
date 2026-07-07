package runtimeagent

import (
	"strings"
	"testing"
)

func TestParseRuntimeDocumentNormalizesRuntimeDefaults(t *testing.T) {
	runtime, err := ParseRuntimeDocument([]byte(`
apiVersion: aifar.io/v1
kind: Runtime
metadata:
  name: demo
  namespace: prod
spec:
  deployments:
    - name: api
      image: demo-api:1
      ports:
        - name: http
          containerPort: 9000
  services:
    - name: api
      selector:
        app: api
      port: 9000
      targetPort: http
      listenPort: 19000
`))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Spec.Network != DefaultNetwork {
		t.Fatalf("expected default network %q, got %q", DefaultNetwork, runtime.Spec.Network)
	}
	if deploymentReplicas(runtime.Spec.Deployments[0]) != 1 {
		t.Fatalf("expected replicas to default to 1, got %d", deploymentReplicas(runtime.Spec.Deployments[0]))
	}
	if runtime.Spec.Services[0].Protocol != "http" || runtime.Spec.Services[0].AffinityPolicy != "none" {
		t.Fatalf("unexpected service defaults: %#v", runtime.Spec.Services[0])
	}
}

func TestParseRuntimeDocumentRejectsNonRenderedRuntimeFields(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "status",
			body: `
apiVersion: aifar.io/v1
kind: Runtime
metadata: {name: demo}
status: {phase: Ready}
spec: {}
`,
			want: "status",
		},
		{
			name: "registry projections",
			body: `
apiVersion: aifar.io/v1
kind: Runtime
metadata: {name: demo}
spec:
  registryProjections: []
`,
			want: "registryProjections",
		},
		{
			name: "template residue",
			body: `
apiVersion: aifar.io/v1
kind: Runtime
metadata: {name: demo}
spec:
  deployments:
    - name: api
      image: "{{ .Values.image }}"
`,
			want: "template",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseRuntimeDocument([]byte(tt.body)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateRuntimeRejectsInvalidContract(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Runtime)
		want   string
	}{
		{
			name: "invalid name",
			mutate: func(runtime *Runtime) {
				runtime.Metadata.Name = "Demo_App"
			},
			want: "metadata.name",
		},
		{
			name: "port conflict",
			mutate: func(runtime *Runtime) {
				runtime.Spec.Ingress[0].ListenPort = runtime.Spec.Services[0].ListenPort
			},
			want: "listenPort",
		},
		{
			name: "invalid node name",
			mutate: func(runtime *Runtime) {
				runtime.Spec.NodeName = "Edge_A"
			},
			want: "nodeName",
		},
		{
			name: "selector no match",
			mutate: func(runtime *Runtime) {
				runtime.Spec.Services[0].Selector = map[string]string{"app": "missing"}
			},
			want: "selector",
		},
		{
			name: "target port no match",
			mutate: func(runtime *Runtime) {
				runtime.Spec.Services[0].TargetPort = FromString("missing")
			},
			want: "targetPort",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			runtime := testRuntime(18080, 19000)
			tt.mutate(&runtime)
			if err := ValidateRuntime(runtime); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateRuntimeAllowsReplicasZero(t *testing.T) {
	runtime := testRuntime(18080, 19000)
	zero := 0
	runtime.Spec.Deployments[0].Replicas = &zero
	if err := ValidateRuntime(runtime); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRuntimeValidatesSecretsStrategyAndResources(t *testing.T) {
	runtime := testRuntime(18080, 19000)
	runtime.Spec.Secrets = []SecretSpec{{
		Name: "regcred",
		Type: "registry-auth",
		StringData: map[string]string{
			"server":   "registry.local",
			"username": "robot",
			"password": "secret",
		},
	}}
	runtime.Spec.Deployments[0].ImagePullSecrets = []LocalObjectReference{{Name: "regcred"}}
	runtime.Spec.Deployments[0].Strategy = DeploymentStrategy{
		Type:          "RollingUpdate",
		RollingUpdate: &RollingUpdateStrategy{MaxSurge: 1},
	}
	runtime.Spec.Deployments[0].Resources = ResourceSpec{CPUs: "0.5", Memory: "256Mi", MemorySwap: "512Mi", PIDsLimit: 128}
	runtime.Spec.Deployments[0].HealthCheck.Interval = "5s"

	if err := ValidateRuntime(runtime); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRuntimeRejectsInvalidSecretAndResourceReferences(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Runtime)
		want   string
	}{
		{
			name: "missing image pull secret",
			mutate: func(runtime *Runtime) {
				runtime.Spec.Deployments[0].ImagePullSecrets = []LocalObjectReference{{Name: "missing"}}
			},
			want: "imagePullSecret",
		},
		{
			name: "invalid memory",
			mutate: func(runtime *Runtime) {
				runtime.Spec.Deployments[0].Resources.Memory = "a lot"
			},
			want: "resources.memory",
		},
		{
			name: "invalid secret data",
			mutate: func(runtime *Runtime) {
				runtime.Spec.Secrets = []SecretSpec{{
					Name: "bad-secret",
					Type: "opaque",
					Data: map[string]string{"PASSWORD": "not base64"},
				}}
			},
			want: "base64",
		},
		{
			name: "invalid health duration",
			mutate: func(runtime *Runtime) {
				runtime.Spec.Deployments[0].HealthCheck.Timeout = "soon"
			},
			want: "healthCheck.timeout",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			runtime := testRuntime(18080, 19000)
			tt.mutate(&runtime)
			if err := ValidateRuntime(runtime); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestLegacyRuntimeSpecIsReadOnlyCompatible(t *testing.T) {
	runtime, err := ParseRuntimeDocument([]byte(`{
  "instanceId": "admin",
  "network": "aifar-network",
  "services": [
    {"name": "gateway", "port": 38000, "listenPort": 38000, "targetPort": 38000}
  ],
  "deployments": [
    {"serviceName": "gateway", "image": "aifar-gateway:test", "replicas": 1, "ports": [{"name": "http", "containerPort": 38000}]}
  ],
  "ingress": {"gatewayService": "gateway", "webService": "gateway", "webPort": 8080}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Metadata.Name != "admin" || runtime.Metadata.Namespace != DefaultNamespace {
		t.Fatalf("unexpected metadata: %#v", runtime.Metadata)
	}
	if len(runtime.Spec.Services) != 1 || runtime.Spec.Services[0].Name != "gateway" {
		t.Fatalf("unexpected converted services: %#v", runtime.Spec.Services)
	}
}
