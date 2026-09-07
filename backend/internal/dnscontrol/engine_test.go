package dnscontrol

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	result   CommandResult
	err      error
	commands []Command
}

const runnerHelperTimeout = 5 * time.Second

func (runner *fakeRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	runner.commands = append(runner.commands, command)

	return runner.result, runner.err
}

func TestHealthRequiresExactDNSControlVersion(t *testing.T) {
	runner := &fakeRunner{
		result: CommandResult{Stdout: []byte("v5.0.3\n")},
	}
	engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: t.TempDir()}, runner)
	if err != nil {
		t.Fatal(err)
	}

	info, err := engine.Health(context.Background())
	if err != nil || info.Version != "v5.0.3" {
		t.Fatalf("health = %#v, %v", info, err)
	}
}

func TestHealthRejectsUnexpectedVersion(t *testing.T) {
	runner := &fakeRunner{
		result: CommandResult{Stdout: []byte("v5.1.0\n")},
	}
	engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: t.TempDir()}, runner)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Health(context.Background()); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestHealthDoesNotAllowConfigVersionOverride(t *testing.T) {
	engine, err := New(Config{
		BinaryPath:      "/trusted/dnscontrol",
		WorkRoot:        t.TempDir(),
		ExpectedVersion: "v5.1.0",
	}, &fakeRunner{result: CommandResult{Stdout: []byte("v5.1.0\n")}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Health(context.Background()); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestHealthUsesTrustedVersionCommand(t *testing.T) {
	workRoot := t.TempDir()
	runner := &fakeRunner{result: CommandResult{Stdout: []byte(ExpectedVersion)}}
	engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: workRoot}, runner)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Health(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	command := runner.commands[0]
	if command.Path != "/trusted/dnscontrol" {
		t.Fatalf("path = %q", command.Path)
	}
	if command.Dir != workRoot {
		t.Fatalf("dir = %q, want %q", command.Dir, workRoot)
	}
	if !reflect.DeepEqual(command.Args, []string{"version"}) {
		t.Fatalf("args = %#v", command.Args)
	}
	if command.Timeout != commandTimeout {
		t.Fatalf("timeout = %s, want %s", command.Timeout, commandTimeout)
	}
}

func TestHealthDoesNotLeakCommandOutput(t *testing.T) {
	const stdoutSecret = "stdout-secret"
	const stderrSecret = "stderr-secret"
	engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: t.TempDir()}, &fakeRunner{
		result: CommandResult{Stdout: []byte(stdoutSecret), Stderr: []byte(stderrSecret)},
		err:    errors.New("command failed"),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Health(context.Background())
	if err == nil {
		t.Fatal("Health error = nil")
	}
	if strings.Contains(err.Error(), stdoutSecret) || strings.Contains(err.Error(), stderrSecret) {
		t.Fatalf("error leaked command output: %q", err)
	}
}

func TestExecRunnerRunsLiteralArgumentsWithClosedStdin(t *testing.T) {
	t.Setenv("GO_WANT_EXEC_RUNNER_HELPER", "1")

	result, err := (execRunner{}).Run(context.Background(), Command{
		Path:    os.Args[0],
		Args:    []string{"-test.run=^TestExecRunnerHelperProcess$", "--", "literal;echo injected"},
		Timeout: runnerHelperTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	if !strings.HasPrefix(string(result.Stdout), "stdin-closed\narg=literal;echo injected\n") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestExecRunnerLimitsOutput(t *testing.T) {
	t.Setenv("GO_WANT_EXEC_RUNNER_HELPER", "1")

	result, err := (execRunner{}).Run(context.Background(), Command{
		Path:    os.Args[0],
		Args:    []string{"-test.run=^TestExecRunnerHelperProcess$", "--", "large-output"},
		Timeout: runnerHelperTimeout,
	})
	if err == nil {
		t.Fatal("Run error = nil")
	}
	if len(result.Stdout) > maxOutputBytes || len(result.Stderr) > maxOutputBytes {
		t.Fatalf("output sizes = stdout:%d stderr:%d", len(result.Stdout), len(result.Stderr))
	}
}

func TestExecRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_RUNNER_HELPER") != "1" {
		return
	}

	for index, arg := range os.Args {
		if arg != "--" || index+1 >= len(os.Args) {
			continue
		}

		switch os.Args[index+1] {
		case "literal;echo injected":
			stdin, err := io.ReadAll(os.Stdin)
			if err != nil {
				os.Exit(1)
			}
			if len(stdin) != 0 {
				fmt.Fprintln(os.Stdout, "stdin-open")
				os.Exit(1)
			}
			fmt.Fprintln(os.Stdout, "stdin-closed")
			fmt.Fprintln(os.Stdout, "arg=literal;echo injected")
			return
		case "large-output":
			payload := bytes.Repeat([]byte("x"), maxOutputBytes+1)
			_, _ = os.Stdout.Write(payload)
			_, _ = os.Stderr.Write(payload)
			return
		}
	}

	os.Exit(1)
}
