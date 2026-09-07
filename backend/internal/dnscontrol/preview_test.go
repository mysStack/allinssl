package dnscontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordingRunner struct {
	report   []byte
	result   CommandResult
	err      error
	results  []CommandResult
	errors   []error
	commands []Command
}

func (runner *recordingRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	runner.commands = append(runner.commands, command)
	index := len(runner.commands) - 1
	result, err := runner.result, runner.err
	if index < len(runner.results) {
		result = runner.results[index]
	}
	if index < len(runner.errors) {
		err = runner.errors[index]
	}
	if len(command.Args) > 0 && command.Args[0] == "preview" && err == nil && result.ExitCode == 0 {
		if err := os.WriteFile(commandArgument(command.Args, "--report"), runner.report, 0o600); err != nil {
			return CommandResult{}, err
		}
	}
	return result, err
}

func TestPreviewRunsOnlyCheckThenPreviewWithFixedFlags(t *testing.T) {
	runner := &recordingRunner{report: fixture(t, "preview-zero-change.json")}
	engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: t.TempDir()}, runner)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := engine.Preview(context.Background(), PreviewInput{Zone: "example.com", Artifacts: validArtifacts()})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Corrections != 0 || plan.Provider != "ALIDNS" {
		t.Fatalf("plan = %#v", plan)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if !reflect.DeepEqual(commandWithoutPaths(runner.commands[0]), []string{"check", "--config", "<path>"}) {
		t.Fatalf("check args = %#v", runner.commands[0].Args)
	}
	if !reflect.DeepEqual(commandWithoutPaths(runner.commands[1]), []string{"preview", "--no-colors", "--config", "<path>", "--creds", "<path>", "--domains", "example.com", "--cmode", "none", "--no-populate", "--report", "<path>"}) {
		t.Fatalf("preview args = %#v", runner.commands[1].Args)
	}
	for _, command := range runner.commands {
		if command.Path != "/trusted/dnscontrol" || command.Dir == "" || contains(command.Args, "push") || !contains(command.Args, "--no-populate") && command.Args[0] == "preview" {
			t.Fatalf("unsafe command = %#v", command)
		}
	}
	if runner.commands[0].Timeout != commandTimeout || runner.commands[1].Timeout != previewTimeout {
		t.Fatalf("timeouts = %s, %s", runner.commands[0].Timeout, runner.commands[1].Timeout)
	}
}

func TestPreviewRejectsCommandFailuresAndUnsafeReports(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		runner *recordingRunner
	}{
		{"check exit", &recordingRunner{result: CommandResult{ExitCode: 1}}},
		{"preview exit", &recordingRunner{results: []CommandResult{{}, {ExitCode: 1}}}},
		{"runner error", &recordingRunner{err: errors.New("runner failed")}},
		{"empty report", &recordingRunner{}},
		{"malformed report", &recordingRunner{report: []byte("{")}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: t.TempDir()}, testCase.runner)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Preview(context.Background(), PreviewInput{Zone: "example.com", Artifacts: validArtifacts()}); err == nil {
				t.Fatal("Preview error = nil")
			}
		})
	}
}

func TestPreviewRejectsInvalidZoneBeforeRunningCommands(t *testing.T) {
	runner := &recordingRunner{report: fixture(t, "preview-zero-change.json")}
	engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: t.TempDir()}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Preview(context.Background(), PreviewInput{Zone: "Example.com.", Artifacts: validArtifacts()}); !errors.Is(err, ErrInvalidZone) {
		t.Fatalf("Preview error = %v, want %v", err, ErrInvalidZone)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v", runner.commands)
	}
}

func TestPreviewCleansWorkspaceAfterCommandFailure(t *testing.T) {
	root := t.TempDir()
	engine, err := New(Config{BinaryPath: "/trusted/dnscontrol", WorkRoot: root}, &recordingRunner{result: CommandResult{ExitCode: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Preview(context.Background(), PreviewInput{Zone: "example.com", Artifacts: validArtifacts()}); err == nil {
		t.Fatal("Preview error = nil")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace entries remain: %v", entries)
	}
}

func TestReadPreviewReportRejectsUnsafeFiles(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.json")
	if _, err := readPreviewReport(missing); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("missing report error = %v", err)
	}

	oversized := filepath.Join(directory, "oversized.json")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), maxReportBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreviewReport(oversized); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("oversized report error = %v", err)
	}

	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, fixture(t, "preview-zero-change.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "report.json")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreviewReport(symlink); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("symlink report error = %v", err)
	}
}

func TestBindFixtureRunsExactCheckAndPreview(t *testing.T) {
	binary, err := exec.LookPath("dnscontrol-5.0.3")
	if err != nil {
		t.Skip("dnscontrol-5.0.3 is not installed")
	}
	version, err := exec.Command(binary, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(version)) != ExpectedVersion {
		t.Fatalf("dnscontrol version = %q, want %q", version, ExpectedVersion)
	}

	fixtureDirectory := filepath.Join("testdata", "bind")
	workDirectory := t.TempDir()
	for _, name := range []string{"dnsconfig.js", "preview.example.zone"} {
		contents, err := os.ReadFile(filepath.Join(fixtureDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workDirectory, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workDirectory, "creds.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zonePath := filepath.Join(workDirectory, "preview.example.zone")
	originalZone, err := os.ReadFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(workDirectory, "report.json")
	if err := os.WriteFile(reportPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	check := exec.Command(binary, "check", "--config", filepath.Join(workDirectory, "dnsconfig.js"))
	check.Dir = workDirectory
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("dnscontrol check: %v\n%s", err, output)
	}
	preview := exec.Command(binary,
		"preview", "--no-colors", "--config", filepath.Join(workDirectory, "dnsconfig.js"),
		"--creds", filepath.Join(workDirectory, "creds.json"), "--domains", "preview.example",
		"--cmode", "none", "--no-populate", "--report", reportPath,
	)
	preview.Dir = workDirectory
	if output, err := preview.CombinedOutput(); err != nil {
		t.Fatalf("dnscontrol preview: %v\n%s", err, output)
	}

	var reportItems []struct {
		Domain      string `json:"domain"`
		Corrections int    `json:"corrections"`
		Provider    string `json:"provider"`
	}
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reportData, &reportItems); err != nil {
		t.Fatal(err)
	}
	foundBind := false
	for _, item := range reportItems {
		if item.Domain == "preview.example" && item.Provider == "bind" {
			foundBind = true
			if item.Corrections != 4 {
				t.Fatalf("bind corrections = %d, want 4", item.Corrections)
			}
		}
	}
	if !foundBind {
		t.Fatalf("bind report missing: %s", reportData)
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %04o, want 0600", info.Mode().Perm())
	}
	updatedZone, err := os.ReadFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(updatedZone, originalZone) {
		t.Fatal("preview modified the bind fixture zone")
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validArtifacts() Artifacts {
	return Artifacts{Config: []byte("config"), Credentials: []byte("credentials")}
}

func commandArgument(arguments []string, flag string) string {
	for index, argument := range arguments {
		if argument == flag && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func commandWithoutPaths(arguments Command) []string {
	result := append([]string(nil), arguments.Args...)
	for index, argument := range result {
		if (argument == "--config" || argument == "--creds" || argument == "--report") && index+1 < len(result) {
			result[index+1] = "<path>"
		}
	}
	return result
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
