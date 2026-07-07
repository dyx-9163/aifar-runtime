package runtimeagent

import (
	"path/filepath"
	"testing"
)

func TestAuditLoggerAppendsAndFiltersEvents(t *testing.T) {
	logger := NewAuditLogger(AuditConfig{
		Enabled:     boolPtr(true),
		Path:        filepath.Join(t.TempDir(), "audit.jsonl"),
		MaxFileSize: 1024 * 1024,
		MaxBackups:  2,
	})
	if logger == nil {
		t.Fatal("expected audit logger")
	}
	if err := logger.Append(AuditEvent{Actor: "alice", Operation: "apply", Namespace: "prod", Name: "api", Result: AuditResultSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Append(AuditEvent{Actor: "bob", Operation: "delete", Namespace: "dev", Name: "worker", Result: AuditResultDenied}); err != nil {
		t.Fatal(err)
	}

	events, err := logger.Read(AuditQuery{Namespace: "prod", Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 filtered audit event, got %d", len(events))
	}
	if events[0].Actor != "alice" || events[0].Operation != "apply" || events[0].Result != AuditResultSucceeded {
		t.Fatalf("unexpected audit event: %#v", events[0])
	}
}

func TestAuditLoggerRotatesAndReadsTail(t *testing.T) {
	logger := NewAuditLogger(AuditConfig{
		Enabled:     boolPtr(true),
		Path:        filepath.Join(t.TempDir(), "audit.jsonl"),
		MaxFileSize: 1,
		MaxBackups:  3,
	})
	if logger == nil {
		t.Fatal("expected audit logger")
	}
	for i := 0; i < 5; i++ {
		if err := logger.Append(AuditEvent{Actor: "alice", Operation: "apply", Namespace: "prod", Name: "api", Result: AuditResultSucceeded, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}

	events, err := logger.Read(AuditQuery{Tail: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected tail of 2 audit events, got %d", len(events))
	}
	for _, event := range events {
		if event.Actor != "alice" || event.Operation != "apply" {
			t.Fatalf("unexpected rotated audit event: %#v", event)
		}
	}
}

func TestAuditLoggerDisabledReturnsNil(t *testing.T) {
	enabled := false
	if logger := NewAuditLogger(AuditConfig{Enabled: &enabled}); logger != nil {
		t.Fatalf("expected nil logger when audit is disabled")
	}
}
