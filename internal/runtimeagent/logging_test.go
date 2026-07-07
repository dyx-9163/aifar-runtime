package runtimeagent

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestStructuredLogWriterEmitsJSONLines(t *testing.T) {
	var out bytes.Buffer
	writer := NewStructuredLogWriter(&out)

	if _, err := writer.Write([]byte("hello runtime\n")); err != nil {
		t.Fatal(err)
	}

	var entry map[string]string
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["message"] != "hello runtime" || entry["level"] != "info" || entry["ts"] == "" {
		t.Fatalf("unexpected structured log entry: %#v", entry)
	}
}
