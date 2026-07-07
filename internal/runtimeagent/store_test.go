package runtimeagent

import (
	"bytes"
	"testing"
)

func TestStateStoreAppendAndReadEvents(t *testing.T) {
	store := NewStateStore(t.TempDir())

	if err := store.AppendEvent("prod", "demo", "Normal", "Applied", "done"); err != nil {
		t.Fatal(err)
	}
	events, err := store.ReadEvents("prod", "demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Reason != "Applied" || events[0].Message != "done" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestStateStoreBackupAndRestore(t *testing.T) {
	source := NewStateStore(t.TempDir())
	runtime := testRuntime(18080, 19000)
	if err := source.SaveRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	status := NewStatus(runtime, RuntimePhaseRunning, nil, nil)
	if err := source.SaveStatus(runtime, status); err != nil {
		t.Fatal(err)
	}
	if err := source.AppendEvent("prod", "demo", "Normal", "Applied", "done"); err != nil {
		t.Fatal(err)
	}

	var backup bytes.Buffer
	if err := source.BackupTo(&backup); err != nil {
		t.Fatal(err)
	}

	target := NewStateStore(t.TempDir())
	if err := target.RestoreFrom(&backup); err != nil {
		t.Fatal(err)
	}
	if _, _, found := NewManager(ManagerOptions{Store: target}).GetRuntime("prod", "demo"); !found {
		t.Fatal("restored runtime not found")
	}
	events, err := target.ReadEvents("prod", "demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Reason != "Applied" {
		t.Fatalf("unexpected restored events: %#v", events)
	}
}
