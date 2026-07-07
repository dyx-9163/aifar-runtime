package runtimeagent

import "testing"

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
