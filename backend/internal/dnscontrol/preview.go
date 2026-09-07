package dnscontrol

import (
	"context"
	"fmt"
	"os"
	"time"

	"ALLinSSL/backend/internal/dnsmodel"
)

const (
	previewTimeout = 60 * time.Second
	maxReportBytes = 1024 * 1024
)

func (engine *Engine) Preview(ctx context.Context, input PreviewInput) (plan PreviewPlan, err error) {
	zone, err := dnsmodel.NormalizeZone(input.Zone)
	if err != nil || zone != input.Zone {
		return PreviewPlan{}, ErrInvalidZone
	}

	workspace, err := PrepareWorkspace(engine.workRoot, "preview", input.Artifacts)
	if err != nil {
		return PreviewPlan{}, err
	}
	defer func() {
		if cleanupErr := workspace.Cleanup(); cleanupErr != nil {
			if err == nil {
				plan = PreviewPlan{}
				err = cleanupErr
				return
			}
			err = fmt.Errorf("%w; cleanup dnscontrol workspace: %v", err, cleanupErr)
		}
	}()

	if err := engine.runPreviewCommand(ctx, workspace, commandTimeout, "check", "--config", workspace.ConfigPath); err != nil {
		return PreviewPlan{}, err
	}
	if err := engine.runPreviewCommand(ctx, workspace, previewTimeout,
		"preview", "--no-colors", "--config", workspace.ConfigPath, "--creds", workspace.CredentialsPath,
		"--domains", zone, "--cmode", "none", "--no-populate", "--report", workspace.ReportPath,
	); err != nil {
		return PreviewPlan{}, err
	}

	report, err := readPreviewReport(workspace.ReportPath)
	if err != nil {
		return PreviewPlan{}, err
	}
	return ParseReport(report, zone)
}

func (engine *Engine) runPreviewCommand(ctx context.Context, workspace Workspace, timeout time.Duration, arguments ...string) error {
	result, err := engine.runner.Run(ctx, Command{
		Path:    engine.binaryPath,
		Dir:     workspace.Dir,
		Args:    arguments,
		Timeout: timeout,
	})
	if err != nil {
		return fmt.Errorf("run dnscontrol %s: %w", arguments[0], err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("dnscontrol %s failed", arguments[0])
	}
	return nil
}

func readPreviewReport(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxReportBytes {
		return nil, ErrInvalidReport
	}
	report, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrInvalidReport
	}
	if len(report) == 0 || len(report) > maxReportBytes {
		return nil, ErrInvalidReport
	}
	return report, nil
}
