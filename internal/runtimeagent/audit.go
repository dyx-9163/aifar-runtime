package runtimeagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	AuditResultSucceeded = "succeeded"
	AuditResultFailed    = "failed"
	AuditResultDenied    = "denied"
)

type AuditEvent struct {
	Time       time.Time `json:"time"`
	RequestID  string    `json:"requestId,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	Role       string    `json:"role,omitempty"`
	SourceIP   string    `json:"sourceIP,omitempty"`
	Method     string    `json:"method,omitempty"`
	Path       string    `json:"path,omitempty"`
	Operation  string    `json:"operation,omitempty"`
	Namespace  string    `json:"namespace,omitempty"`
	Name       string    `json:"name,omitempty"`
	Result     string    `json:"result,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	StatusCode int       `json:"statusCode,omitempty"`
	DurationMS int64     `json:"durationMs,omitempty"`
}

type AuditQuery struct {
	Tail      int
	Namespace string
	Name      string
	Actor     string
	Operation string
	Result    string
}

type AuditLogger struct {
	config AuditConfig
	mu     sync.Mutex
}

func NewAuditLogger(config AuditConfig) *AuditLogger {
	config = normalizeAuditConfig(config)
	if config.Enabled == nil || !*config.Enabled {
		return nil
	}
	return &AuditLogger{config: config}
}

func (l *AuditLogger) IncludeReadOnly() bool {
	return l != nil && l.config.IncludeReadOnly
}

func (l *AuditLogger) Append(event AuditEvent) error {
	if l == nil {
		return nil
	}
	event = normalizeAuditEvent(event)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.config.Path), 0o750); err != nil {
		return fmt.Errorf("create audit log directory: %w", err)
	}
	if err := l.rotateIfNeeded(); err != nil {
		return err
	}
	file, err := os.OpenFile(l.config.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

func (l *AuditLogger) Read(query AuditQuery) ([]AuditEvent, error) {
	if l == nil {
		return nil, nil
	}
	query = normalizeAuditQuery(query)
	l.mu.Lock()
	defer l.mu.Unlock()
	events := []AuditEvent{}
	for _, path := range l.readPaths() {
		if err := readAuditFile(path, query, &events); err != nil {
			return nil, err
		}
		if query.Tail > 0 && len(events) > query.Tail {
			events = append([]AuditEvent(nil), events[len(events)-query.Tail:]...)
		}
	}
	return events, nil
}

func (l *AuditLogger) rotateIfNeeded() error {
	info, err := os.Stat(l.config.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat audit log: %w", err)
	}
	if info.Size() < l.config.MaxFileSize {
		return nil
	}
	if l.config.MaxBackups <= 0 {
		if err := os.Truncate(l.config.Path, 0); err != nil {
			return fmt.Errorf("truncate audit log: %w", err)
		}
		return nil
	}
	for i := l.config.MaxBackups; i >= 1; i-- {
		source := auditBackupPath(l.config.Path, i)
		if i == l.config.MaxBackups {
			if err := removeIfExists(source); err != nil {
				return err
			}
			continue
		}
		target := auditBackupPath(l.config.Path, i+1)
		if err := renameIfExists(source, target); err != nil {
			return err
		}
	}
	if err := renameIfExists(l.config.Path, auditBackupPath(l.config.Path, 1)); err != nil {
		return err
	}
	return nil
}

func (l *AuditLogger) readPaths() []string {
	paths := make([]string, 0, l.config.MaxBackups+1)
	for i := l.config.MaxBackups; i >= 1; i-- {
		paths = append(paths, auditBackupPath(l.config.Path, i))
	}
	paths = append(paths, l.config.Path)
	return paths
}

func readAuditFile(path string, query AuditQuery, events *[]AuditEvent) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open audit log %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return fmt.Errorf("parse audit log %s: %w", path, err)
		}
		if !auditEventMatches(event, query) {
			continue
		}
		*events = append(*events, event)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read audit log %s: %w", path, err)
	}
	return nil
}

func auditEventMatches(event AuditEvent, query AuditQuery) bool {
	if query.Namespace != "" && event.Namespace != query.Namespace {
		return false
	}
	if query.Name != "" && event.Name != query.Name {
		return false
	}
	if query.Actor != "" && event.Actor != query.Actor {
		return false
	}
	if query.Operation != "" && event.Operation != query.Operation {
		return false
	}
	if query.Result != "" && event.Result != query.Result {
		return false
	}
	return true
}

func normalizeAuditEvent(event AuditEvent) AuditEvent {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	} else {
		event.Time = event.Time.UTC()
	}
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.Actor = strings.TrimSpace(event.Actor)
	event.Role = strings.TrimSpace(event.Role)
	event.SourceIP = strings.TrimSpace(event.SourceIP)
	event.Method = strings.TrimSpace(event.Method)
	event.Path = strings.TrimSpace(event.Path)
	event.Operation = strings.TrimSpace(event.Operation)
	event.Namespace = strings.TrimSpace(event.Namespace)
	event.Name = strings.TrimSpace(event.Name)
	event.Result = strings.TrimSpace(event.Result)
	event.Reason = strings.TrimSpace(event.Reason)
	return event
}

func normalizeAuditQuery(query AuditQuery) AuditQuery {
	query.Namespace = strings.TrimSpace(query.Namespace)
	query.Name = strings.TrimSpace(query.Name)
	query.Actor = strings.TrimSpace(query.Actor)
	query.Operation = strings.TrimSpace(query.Operation)
	query.Result = strings.TrimSpace(query.Result)
	return query
}

func auditBackupPath(path string, index int) string {
	return fmt.Sprintf("%s.%d", path, index)
}

func renameIfExists(source, target string) error {
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat audit log %s: %w", source, err)
	}
	if err := removeIfExists(target); err != nil {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("rotate audit log %s to %s: %w", source, target, err)
	}
	return nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove audit log %s: %w", path, err)
	}
	return nil
}
