//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRuntimeAPIReadinessAndMetadata(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := integrationBaseURL(t)
	getJSON(t, client, baseURL+"/healthz", nil)
	getJSON(t, client, baseURL+"/version", integrationHeaders())
	getJSON(t, client, baseURL+"/status", integrationHeaders())
}

func TestRuntimeAPIValidateOnly(t *testing.T) {
	if os.Getenv("AIFAR_RUNTIME_INTEGRATION_VALIDATE") != "1" {
		t.Skip("set AIFAR_RUNTIME_INTEGRATION_VALIDATE=1 to run validate API integration test")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := integrationBaseURL(t)
	body := []byte(`
apiVersion: aifar.io/v1
kind: Runtime
metadata:
  name: validate-demo
  namespace: integration
spec:
  deployments:
    - name: api
      image: example.invalid/aifar-runtime-api:0.0.0
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
      listenPort: 19090
`)
	url := baseURL + "/apis/aifar.io/v1/namespaces/integration/runtimes/validate-demo:validate"
	headers := integrationHeaders()
	headers.Set("Content-Type", "application/yaml")
	postJSON(t, client, url, headers, body)
}

func integrationBaseURL(t *testing.T) string {
	t.Helper()
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AIFAR_RUNTIME_INTEGRATION_ADDR")), "/")
	if baseURL == "" {
		t.Skip("set AIFAR_RUNTIME_INTEGRATION_ADDR to run integration tests against a live runtime")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	return baseURL
}

func integrationHeaders() http.Header {
	headers := http.Header{}
	token := strings.TrimSpace(os.Getenv("AIFAR_RUNTIME_INTEGRATION_TOKEN"))
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	return headers
}

func getJSON(t *testing.T, client *http.Client, target string, headers http.Header) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	return doIntegrationJSON(t, client, req)
}

func postJSON(t *testing.T, client *http.Client, target string, headers http.Header, body []byte) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	return doIntegrationJSON(t, client, req)
}

func doIntegrationJSON(t *testing.T, client *http.Client, req *http.Request) map[string]any {
	t.Helper()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s failed: %s: %s", req.Method, req.URL, resp.Status, strings.TrimSpace(string(data)))
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s %s response: %v\n%s", req.Method, req.URL, err, string(data))
	}
	if len(value) == 0 {
		t.Fatalf("%s %s returned empty JSON object", req.Method, req.URL)
	}
	fmt.Fprint(io.Discard, value)
	return value
}
