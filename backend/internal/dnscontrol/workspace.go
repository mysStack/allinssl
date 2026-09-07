package dnscontrol

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	workspaceDirectoryMode fs.FileMode = 0o700
	workspaceFileMode      fs.FileMode = 0o600
)

var openWorkspaceFile = os.OpenFile

func PrepareWorkspace(root, jobID string, artifacts Artifacts) (Workspace, error) {
	if !validJobID(jobID) {
		return Workspace{}, ErrInvalidJobID
	}

	directory, err := os.MkdirTemp(root, jobID+"-")
	if err != nil {
		return Workspace{}, fmt.Errorf("create dnscontrol workspace: %w", err)
	}
	workspace := Workspace{
		Dir:             directory,
		ConfigPath:      filepath.Join(directory, "dnsconfig.js"),
		CredentialsPath: filepath.Join(directory, "creds.json"),
		ReportPath:      filepath.Join(directory, "report.json"),
	}
	if err := os.Chmod(workspace.Dir, workspaceDirectoryMode); err != nil {
		return Workspace{}, cleanupAfterPrepareFailure(workspace, err)
	}
	for _, file := range []struct {
		path     string
		contents []byte
	}{
		{workspace.ConfigPath, artifacts.Config},
		{workspace.CredentialsPath, artifacts.Credentials},
		{workspace.ReportPath, nil},
	} {
		if err := workspace.writeFile(file.path, file.contents); err != nil {
			return Workspace{}, cleanupAfterPrepareFailure(workspace, err)
		}
	}

	return workspace, nil
}

func (workspace Workspace) Cleanup() error {
	if err := os.Remove(workspace.CredentialsPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove dnscontrol credentials: %w", err)
	}
	if err := os.RemoveAll(workspace.Dir); err != nil {
		return fmt.Errorf("remove dnscontrol workspace: %w", err)
	}

	return nil
}

func (workspace Workspace) writeFile(path string, contents []byte) error {
	file, err := openWorkspaceFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, workspaceFileMode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(contents)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}

	return closeErr
}

func cleanupAfterPrepareFailure(workspace Workspace, cause error) error {
	if cleanupErr := workspace.Cleanup(); cleanupErr != nil {
		return fmt.Errorf("%w; cleanup dnscontrol workspace: %v", cause, cleanupErr)
	}

	return cause
}

func validJobID(jobID string) bool {
	if jobID == "" {
		return false
	}
	for _, character := range jobID {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}

	return true
}
