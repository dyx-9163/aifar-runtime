package runtimeagent

import (
	"net/http/httptest"
	"testing"
)

func TestAffinityKeyForRequestUsesPolicy(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.test/upload", nil)
	req.RemoteAddr = "192.0.2.10:12345"

	if got := affinityKeyForRequest(ServiceSpec{AffinityPolicy: "client-ip"}, req); got != "remote:192.0.2.10" {
		t.Fatalf("expected client-ip affinity key, got %q", got)
	}
	req.Header.Set("X-AIFAR-Affinity", "upload-1")
	if got := affinityKeyForRequest(ServiceSpec{AffinityPolicy: "header"}, req); got != "X-AIFAR-Affinity:upload-1" {
		t.Fatalf("expected header affinity key, got %q", got)
	}
}
