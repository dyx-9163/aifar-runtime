package runtimeagent

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

type structuredLogWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func NewStructuredLogWriter(out io.Writer) io.Writer {
	if out == nil {
		return io.Discard
	}
	return &structuredLogWriter{out: out}
}

func (w *structuredLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range strings.Split(string(data), "\n") {
		message := strings.TrimSpace(line)
		if message == "" {
			continue
		}
		entry := map[string]string{
			"ts":      time.Now().UTC().Format(time.RFC3339Nano),
			"level":   "info",
			"message": message,
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return 0, err
		}
		if _, err := w.out.Write(append(encoded, '\n')); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}
