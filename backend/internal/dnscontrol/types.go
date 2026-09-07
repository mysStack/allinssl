package dnscontrol

import (
	"context"
	"errors"
	"time"
)

const ExpectedVersion = "v5.0.3"

var (
	ErrVersionMismatch       = errors.New("dnscontrol version mismatch")
	ErrIncompatibleSnapshot  = errors.New("dnscontrol snapshot is incompatible")
	ErrInvalidZone           = errors.New("dnscontrol zone is invalid")
	ErrInvalidCredentials    = errors.New("dnscontrol credentials are invalid")
	ErrInvalidJobID          = errors.New("dnscontrol job ID is invalid")
	ErrInvalidReport         = errors.New("dnscontrol preview report is invalid")
	ErrUnrepresentableRecord = errors.New("dnscontrol record is unrepresentable")
)

type Command struct {
	Path    string
	Dir     string
	Args    []string
	Timeout time.Duration
}

type CommandResult struct {
	ExitCode       int
	Stdout, Stderr []byte
}

type Runner interface {
	Run(context.Context, Command) (CommandResult, error)
}

type Config struct {
	BinaryPath, WorkRoot, ExpectedVersion string
}

type EngineInfo struct {
	Version string
}

type Workspace struct {
	Dir             string
	ConfigPath      string
	CredentialsPath string
	ReportPath      string
}

type PreviewInput struct {
	Zone      string
	Artifacts Artifacts
}

type PreviewPlan struct {
	Zone        string
	Provider    string
	Corrections int
	Details     []string
}
