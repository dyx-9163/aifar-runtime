package runtimeagent

import (
	"context"
	"os/exec"
	"strings"
)

type CommandResult struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

type InputCommandRunner interface {
	RunWithInput(ctx context.Context, input string, name string, args ...string) (CommandResult, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return CommandResult{Stdout: string(out), Stderr: string(exitErr.Stderr)}, err
		}
		return CommandResult{Stdout: string(out)}, err
	}
	return CommandResult{Stdout: string(out)}, nil
}

func (ExecRunner) RunWithInput(ctx context.Context, input string, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return CommandResult{Stdout: string(out), Stderr: string(exitErr.Stderr)}, err
		}
		return CommandResult{Stdout: string(out)}, err
	}
	return CommandResult{Stdout: string(out)}, nil
}
