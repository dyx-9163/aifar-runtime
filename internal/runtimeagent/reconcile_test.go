package runtimeagent

import (
	"context"
	"testing"
	"time"
)

func TestStartRuntimeResyncReturnsWhenContextIsCanceled(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: newFakeDockerRunner()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manager.StartRuntimeResync(ctx, time.Hour)
}
