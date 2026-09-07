package dnscontrol

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	commandTimeout = 10 * time.Second
	maxOutputBytes = 64 * 1024
)

type Engine struct {
	binaryPath string
	workRoot   string
	runner     Runner
}

func New(config Config, runner Runner) (*Engine, error) {
	if config.BinaryPath == "" {
		return nil, errors.New("dnscontrol binary path is required")
	}
	if config.WorkRoot == "" {
		return nil, errors.New("dnscontrol work root is required")
	}
	if runner == nil {
		runner = execRunner{}
	}

	return &Engine{
		binaryPath: config.BinaryPath,
		workRoot:   config.WorkRoot,
		runner:     runner,
	}, nil
}

func (engine *Engine) Health(ctx context.Context) (EngineInfo, error) {
	result, err := engine.runner.Run(ctx, Command{
		Path:    engine.binaryPath,
		Dir:     engine.workRoot,
		Args:    []string{"version"},
		Timeout: commandTimeout,
	})
	if err != nil {
		return EngineInfo{}, fmt.Errorf("run dnscontrol version: %w", err)
	}

	version := strings.TrimSpace(string(result.Stdout))
	if version != ExpectedVersion {
		return EngineInfo{}, fmt.Errorf("%w", ErrVersionMismatch)
	}

	return EngineInfo{Version: version}, nil
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if command.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, command.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdin = nil

	stdout := &limitedBuffer{limit: maxOutputBytes}
	stderr := &limitedBuffer{limit: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	result := CommandResult{
		ExitCode: exitCode(cmd),
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
	}
	if err != nil {
		return result, err
	}

	return result, nil
}

func exitCode(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return -1
	}

	return cmd.ProcessState.ExitCode()
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		return 0, errors.New("command output exceeds limit")
	}
	if len(data) > remaining {
		_, _ = buffer.buffer.Write(data[:remaining])
		return remaining, errors.New("command output exceeds limit")
	}

	return buffer.buffer.Write(data)
}

func (buffer *limitedBuffer) Bytes() []byte {
	return buffer.buffer.Bytes()
}
