package runtimeagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type RuntimeStateStore interface {
	Root() string
	Ensure() error
	SaveRuntime(runtime Runtime) error
	ReadRuntime(namespace, name string) (Runtime, error)
	LoadAll() ([]Runtime, error)
	SaveStatus(runtime Runtime, status RuntimeStatus) error
	ReadStatus(namespace, name string) (RuntimeStatus, error)
	AppendEvent(namespace, name, eventType, reason, message string) error
	ReadEvents(namespace, name string, tail int) ([]RuntimeEvent, error)
	DeleteRuntime(namespace, name string) error
	BackupTo(writer io.Writer) error
	RestoreFrom(reader io.Reader) error
}

type StateBackup struct {
	Version   string                    `json:"version"`
	CreatedAt string                    `json:"createdAt"`
	Runtimes  []Runtime                 `json:"runtimes"`
	Statuses  map[string]RuntimeStatus  `json:"statuses,omitempty"`
	Events    map[string][]RuntimeEvent `json:"events,omitempty"`
}

type StateStore struct {
	root string
}

func NewStateStore(root string) *StateStore {
	root = strings.TrimSpace(root)
	if root == "" {
		root = DefaultStateDir
	}
	return &StateStore{root: root}
}

func (s *StateStore) Root() string {
	return s.root
}

func (s *StateStore) Ensure() error {
	for _, dir := range []string{"specs", "status", "events", "locks", "proxy"} {
		if err := os.MkdirAll(filepath.Join(s.root, dir), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *StateStore) SaveRuntime(runtime Runtime) error {
	runtime = NormalizeRuntime(runtime)
	runtime.Status = nil
	if err := s.Ensure(); err != nil {
		return err
	}
	path := s.specPath(runtime.Metadata.Namespace, runtime.Metadata.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, MustJSONRuntime(runtime), 0o644)
}

func (s *StateStore) ReadRuntime(namespace, name string) (Runtime, error) {
	key := runtimeKey(namespace, name)
	data, err := os.ReadFile(s.specPath(key.Namespace, key.Name))
	if err != nil {
		return Runtime{}, err
	}
	return ParseRuntimeDocument(data)
}

func (s *StateStore) LoadAll() ([]Runtime, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	runtimes := []Runtime{}
	specRoot := filepath.Join(s.root, "specs")
	if err := filepath.WalkDir(specRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		runtime, err := ParseRuntimeDocument(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		runtimes = append(runtimes, runtime)
		return nil
	}); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	legacy, err := s.loadLegacySpecs()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, runtime := range runtimes {
		seen[KeyForRuntime(runtime).String()] = true
	}
	for _, runtime := range legacy {
		if !seen[KeyForRuntime(runtime).String()] {
			runtimes = append(runtimes, runtime)
		}
	}
	sort.Slice(runtimes, func(i, j int) bool {
		return KeyForRuntime(runtimes[i]).String() < KeyForRuntime(runtimes[j]).String()
	})
	return runtimes, nil
}

func (s *StateStore) SaveStatus(runtime Runtime, status RuntimeStatus) error {
	runtime = NormalizeRuntime(runtime)
	if err := s.Ensure(); err != nil {
		return err
	}
	path := s.statusPath(runtime.Metadata.Namespace, runtime.Metadata.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (s *StateStore) ReadStatus(namespace, name string) (RuntimeStatus, error) {
	key := runtimeKey(namespace, name)
	var status RuntimeStatus
	data, err := os.ReadFile(s.statusPath(key.Namespace, key.Name))
	if err != nil {
		return status, err
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return status, err
	}
	return status, nil
}

func (s *StateStore) AppendEvent(namespace, name, eventType, reason, message string) error {
	key := runtimeKey(namespace, name)
	event := RuntimeEvent{
		Time:      time.Now().UTC().Format(time.RFC3339Nano),
		Type:      strings.TrimSpace(eventType),
		Reason:    strings.TrimSpace(reason),
		Message:   strings.TrimSpace(message),
		Namespace: key.Namespace,
		Name:      key.Name,
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	path := s.eventsPath(key.Namespace, key.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *StateStore) ReadEvents(namespace, name string, tail int) ([]RuntimeEvent, error) {
	key := runtimeKey(namespace, name)
	file, err := os.Open(s.eventsPath(key.Namespace, key.Name))
	if err != nil {
		if os.IsNotExist(err) {
			return []RuntimeEvent{}, nil
		}
		return nil, err
	}
	defer file.Close()
	events := []RuntimeEvent{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event RuntimeEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if tail > 0 && len(events) > tail {
		events = events[len(events)-tail:]
	}
	return events, nil
}

func (s *StateStore) DeleteRuntime(namespace, name string) error {
	key := runtimeKey(namespace, name)
	paths := []string{
		s.specPath(key.Namespace, key.Name),
		s.statusPath(key.Namespace, key.Name),
		s.lockPath(key.Namespace, key.Name),
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *StateStore) BackupTo(writer io.Writer) error {
	if writer == nil {
		return errors.New("backup writer is required")
	}
	runtimes, err := s.LoadAll()
	if err != nil {
		return err
	}
	backup := StateBackup{
		Version:   RuntimeVersion,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Runtimes:  runtimes,
		Statuses:  map[string]RuntimeStatus{},
		Events:    map[string][]RuntimeEvent{},
	}
	for _, runtime := range runtimes {
		key := KeyForRuntime(runtime)
		if status, err := s.ReadStatus(key.Namespace, key.Name); err == nil {
			backup.Statuses[key.String()] = status
		}
		if events, err := s.ReadEvents(key.Namespace, key.Name, 0); err == nil && len(events) > 0 {
			backup.Events[key.String()] = events
		}
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(backup)
}

func (s *StateStore) RestoreFrom(reader io.Reader) error {
	if reader == nil {
		return errors.New("restore reader is required")
	}
	var backup StateBackup
	if err := json.NewDecoder(reader).Decode(&backup); err != nil {
		return err
	}
	if backup.Version == "" || len(backup.Runtimes) == 0 {
		return errors.New("invalid AIFAR Runtime backup")
	}
	for _, runtime := range backup.Runtimes {
		runtime = NormalizeRuntime(runtime)
		key := KeyForRuntime(runtime)
		if err := ValidateRuntime(runtime); err != nil {
			return fmt.Errorf("restore runtime %s: %w", key.String(), err)
		}
		if err := s.SaveRuntime(runtime); err != nil {
			return err
		}
		if status, ok := backup.Statuses[key.String()]; ok {
			if err := s.SaveStatus(runtime, status); err != nil {
				return err
			}
		}
		if events := backup.Events[key.String()]; len(events) > 0 {
			if err := s.writeEvents(key.Namespace, key.Name, events); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *StateStore) writeEvents(namespace, name string, events []RuntimeEvent) error {
	key := runtimeKey(namespace, name)
	if err := s.Ensure(); err != nil {
		return err
	}
	path := s.eventsPath(key.Namespace, key.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, event := range events {
		event.Namespace = key.Namespace
		event.Name = key.Name
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (s *StateStore) specPath(namespace, name string) string {
	key := runtimeKey(namespace, name)
	return filepath.Join(s.root, "specs", key.Namespace, key.Name+".json")
}

func (s *StateStore) statusPath(namespace, name string) string {
	key := runtimeKey(namespace, name)
	return filepath.Join(s.root, "status", key.Namespace, key.Name+".json")
}

func (s *StateStore) eventsPath(namespace, name string) string {
	key := runtimeKey(namespace, name)
	return filepath.Join(s.root, "events", key.Namespace, key.Name+".jsonl")
}

func (s *StateStore) lockPath(namespace, name string) string {
	key := runtimeKey(namespace, name)
	return filepath.Join(s.root, "locks", key.Namespace, key.Name+".lock")
}

func (s *StateStore) loadLegacySpecs() ([]Runtime, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	runtimes := []Runtime{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		switch entry.Name() {
		case "specs", "status", "events", "locks", "proxy":
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, entry.Name(), "runtime-spec.json"))
		if err != nil {
			continue
		}
		runtime, err := ParseRuntimeDocument(data)
		if err != nil {
			continue
		}
		runtimes = append(runtimes, runtime)
	}
	return runtimes, nil
}
