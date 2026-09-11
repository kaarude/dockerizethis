package verify

import (
	"context"
	"fmt"
	"io"

	"github.com/carl/dockerizethis/internal/plan"
)

type Mode string

const (
	ModeNone  Mode = "none"
	ModeBuild Mode = "build"
	ModeFull  Mode = "full"
)

func ParseMode(value string) (Mode, error) {
	switch value {
	case string(ModeNone), string(ModeBuild), string(ModeFull):
		return Mode(value), nil
	default:
		return "", fmt.Errorf("invalid --verify value %q: expected none, build, or full", value)
	}
}

type Options struct {
	Mode     Mode
	Progress io.Writer
}

// Report describes the requested verification as a whole. Passed means every
// required step, including temporary-container cleanup, completed successfully.
type Report struct {
	Mode   Mode         `json:"mode"`
	Status string       `json:"status"`
	Reason string       `json:"reason,omitempty"`
	Build  *BuildResult `json:"build,omitempty"`
	Smoke  *SmokeResult `json:"smoke,omitempty"`
	Error  string       `json:"error,omitempty"`
}

// Run owns build-to-smoke sequencing. It builds once, keeps transient image
// identity out of the detection plan, and skips startup probes for non-web plans.
func Run(ctx context.Context, dir string, p plan.Plan, opts Options) (report Report, runErr error) {
	report = Report{Mode: opts.Mode, Status: "failed"}
	defer func() {
		if runErr != nil {
			report.Error = runErr.Error()
		}
	}()
	if _, err := ParseMode(string(opts.Mode)); err != nil {
		return report, err
	}
	if opts.Mode == ModeNone {
		report.Status, report.Reason = "skipped", "--verify=none"
		return report, nil
	}
	progress := opts.Progress
	if progress == nil {
		progress = io.Discard
	}
	if _, err := fmt.Fprintln(progress, "Building Docker image..."); err != nil {
		return report, fmt.Errorf("write build progress: %w", err)
	}
	built, err := build(ctx, dir, "Dockerfile")
	report.Build = &built
	if err != nil {
		return report, err
	}
	if opts.Mode == ModeFull {
		if p.Process != plan.ProcessWeb {
			report.Reason = "smoke skipped: only web processes are probed"
		} else {
			if _, err := fmt.Fprintln(progress, "Checking HTTP startup..."); err != nil {
				return report, fmt.Errorf("write smoke progress: %w", err)
			}
			checked, err := smoke(ctx, dir, p, built.Image)
			report.Smoke = &checked
			if err != nil {
				return report, err
			}
		}
	}
	report.Status = "passed"
	return report, nil
}
