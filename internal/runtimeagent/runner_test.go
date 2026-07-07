package runtimeagent

import "testing"

func TestExecRunnerImplementsCommandRunner(t *testing.T) {
	var _ CommandRunner = ExecRunner{}
}
