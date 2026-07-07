package runtimeagent

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

func (m *Manager) StartDockerEventSync(ctx context.Context, debounce time.Duration) {
	if debounce <= 0 {
		debounce = m.config.Docker.EventDebounce.Duration
	}
	for ctx.Err() == nil {
		if err := m.watchDockerEvents(ctx, debounce); err != nil && ctx.Err() == nil {
			logf(m.log, "AIFAR Docker event watcher stopped: %v\n", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.config.Docker.EventBackoff.Duration):
			}
		}
	}
}

func (m *Manager) watchDockerEvents(ctx context.Context, debounce time.Duration) error {
	stdout, stderr, wait, err := m.dockerEvents(ctx)
	if err != nil {
		return err
	}
	defer stdout.Close()
	defer stderr.Close()
	go func() {
		data, _ := io.ReadAll(stderr)
		if len(strings.TrimSpace(string(data))) > 0 {
			logf(m.log, "AIFAR Docker events stderr: %s\n", strings.TrimSpace(string(data)))
		}
	}()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			_ = wait()
			return ctx.Err()
		case <-time.After(debounce):
		}
		if err := m.Resync(ctx); err != nil {
			logf(m.log, "AIFAR runtime Docker event resync failed: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		_ = wait()
		return err
	}
	return wait()
}

func defaultDockerEventWatcher(ctx context.Context, dockerCommand string) (io.ReadCloser, io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, dockerCommand, "events",
		"--filter", "type=container",
		"--filter", "label=aifar.runtime/managed=true",
		"--format", "{{.TimeNano}} {{.Action}} {{.Actor.Attributes.aifar.runtime/name}}",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return stdout, stderr, cmd.Wait, nil
}
