package dnscontrol

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareWorkspaceUsesPrivateModesAndCleansCredentials(t *testing.T) {
	workspace, err := PrepareWorkspace(t.TempDir(), "job-123", Artifacts{
		Config:      []byte("config"),
		Credentials: []byte("secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, workspace.Dir, 0o700)
	assertMode(t, workspace.ConfigPath, 0o600)
	assertMode(t, workspace.CredentialsPath, 0o600)
	assertMode(t, workspace.ReportPath, 0o600)

	if err := workspace.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace.CredentialsPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("credentials remain: %v", err)
	}
}

func TestPrepareWorkspaceRejectsUnsafeJobIDsWithoutCreatingFiles(t *testing.T) {
	root := t.TempDir()
	for _, jobID := range []string{"../job", "job/next", "job space", "job\\next", ""} {
		t.Run(jobID, func(t *testing.T) {
			_, err := PrepareWorkspace(root, jobID, Artifacts{Config: []byte("config"), Credentials: []byte("secret")})
			if !errors.Is(err, ErrInvalidJobID) {
				t.Fatalf("PrepareWorkspace error = %v, want %v", err, ErrInvalidJobID)
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected workspace entries: %v", entries)
	}
}

func TestPrepareWorkspaceDoesNotReuseExistingReport(t *testing.T) {
	workspace, err := PrepareWorkspace(t.TempDir(), "job-123", Artifacts{Config: []byte("config"), Credentials: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Cleanup(); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(workspace.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace.ReportPath, []byte("old report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workspace.writeFile(workspace.ReportPath, nil); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("writeFile error = %v, want %v", err, fs.ErrExist)
	}
	contents, err := os.ReadFile(workspace.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old report" {
		t.Fatalf("report was overwritten: %q", contents)
	}
}

func TestPrepareWorkspaceCleansUpWhenWriteFails(t *testing.T) {
	root := t.TempDir()
	originalOpenFile := openWorkspaceFile
	t.Cleanup(func() { openWorkspaceFile = originalOpenFile })
	openCalls := 0
	openWorkspaceFile = func(path string, flag int, perm fs.FileMode) (*os.File, error) {
		openCalls++
		if openCalls == 2 {
			return nil, fs.ErrPermission
		}
		return originalOpenFile(path, flag, perm)
	}

	_, err := PrepareWorkspace(root, "job-123", Artifacts{Config: []byte("config"), Credentials: []byte("secret")})
	if err == nil {
		t.Fatal("PrepareWorkspace error = nil")
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "job-123-") {
			t.Fatalf("partial workspace remains: %s", filepath.Join(root, entry.Name()))
		}
	}
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %04o, want %04o", path, got, want)
	}
}
