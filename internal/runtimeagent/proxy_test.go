package runtimeagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteJSONFileCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy", "services.json")

	if err := writeJSONFile(path, map[string]string{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "ok"`) {
		t.Fatalf("unexpected JSON file content: %s", string(data))
	}
}
