package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/carl/dockerizethis/internal/detect"
	golangdetect "github.com/carl/dockerizethis/internal/detect/golang"
	"github.com/carl/dockerizethis/internal/detect/node"
	pythondetect "github.com/carl/dockerizethis/internal/detect/python"
	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/common"
	golangrender "github.com/carl/dockerizethis/internal/templates/golang"
	noderender "github.com/carl/dockerizethis/internal/templates/node"
	pythonrender "github.com/carl/dockerizethis/internal/templates/python"
	"github.com/carl/dockerizethis/internal/verify"
)

type options struct {
	dryRun  bool
	yes     bool
	json    bool
	verify  string
	stack   string
	service string
	force   bool
	backup  bool
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(verify.Code(err))
	}
}

func newRootCommand() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:           "dockerizethis [path]",
		Short:         "Generate verified Docker hosting artifacts for a project",
		Long:          "Detect how a project runs, generate Docker hosting artifacts (Dockerfile, .dockerignore, docker-compose.yml, .env.example, a GHCR workflow, DEPLOY.md), and verify the image builds.\nThe path defaults to \".\".",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch opts.verify {
			case "none", "build", "full":
			default:
				return fmt.Errorf("invalid --verify value %q: expected none, build, or full", opts.verify)
			}
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			return run(cmd, path, opts)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.dryRun, "dry-run", false, "Preview artifacts without writing files")
	flags.BoolVar(&opts.yes, "yes", false, "Accept prompts without interaction")
	flags.BoolVar(&opts.json, "json", false, "Print the report as JSON")
	flags.StringVar(&opts.verify, "verify", "build", "Verification level: none, build, or full")
	flags.StringVar(&opts.stack, "stack", "", "Override the detected stack")
	flags.StringVar(&opts.service, "service", "", "Select a service subdirectory in a monorepo")
	flags.BoolVar(&opts.force, "force", false, "Allow replacing existing artifact files")
	flags.BoolVar(&opts.backup, "backup", false, "Back up existing artifact files before replacement")
	return cmd
}

// detectors runs in registry order; the highest-confidence plan wins.
var detectors = []detect.Detector{node.Detector{}, golangdetect.Detector{}, pythondetect.Detector{}}

// report is the --json document and the source of the text summary.
type report struct {
	Path   string         `json:"path"`
	Plan   plan.Plan      `json:"plan"`
	Files  []emit.Result  `json:"files,omitempty"`
	Verify *verifyOutcome `json:"verify,omitempty"`
}

type verifyOutcome struct {
	Level string              `json:"level"`
	Build *verify.Result      `json:"build,omitempty"`
	Smoke *verify.SmokeResult `json:"smoke,omitempty"`
}

func run(cmd *cobra.Command, path string, opts options) error {
	root, err := projectRoot(path, opts.service)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	p, err := detectPlan(root, opts.stack)
	if err != nil {
		return err
	}
	if p.Process == "" {
		return fmt.Errorf("could not determine how this project runs: %s", strings.Join(p.Notes, "; "))
	}

	files, err := renderAll(p)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if p.Stack == "node" {
		p.Notes = append(p.Notes, noderender.RenderNotes(p)...)
	}
	rep := report{Path: root, Plan: p}

	if !opts.dryRun && !opts.yes && interactive(cmd) {
		if err := confirm(cmd, root, p, files); err != nil {
			return err
		}
	}
	if opts.json {
		// Keep stdout pure JSON: dry-run diffs go to stderr instead.
		prev := emit.SetDiffWriter(cmd.ErrOrStderr())
		defer emit.SetDiffWriter(prev)
	}
	rep.Files, err = emit.Write(root, files, emit.Options{
		DryRun: opts.dryRun, Force: opts.force, Backup: opts.backup,
	})
	if err != nil {
		printReport(cmd, rep, opts.json)
		return fmt.Errorf("emit: %w", err)
	}

	var verr error
	if !opts.dryRun && opts.verify != "none" {
		rep.Verify = &verifyOutcome{Level: opts.verify}
		switch opts.verify {
		case "build":
			res, err := verify.Build(ctx, root, verify.DefaultDockerfile)
			rep.Verify.Build = &res
			verr = err
		case "full":
			res, err := verify.Smoke(ctx, root, p)
			rep.Verify.Smoke = &res
			verr = err
		}
	}
	printReport(cmd, rep, opts.json)
	if verr != nil {
		printVerifyDiagnostics(cmd, rep.Verify)
		return verr
	}
	return nil
}

// projectRoot joins --service onto the project path, rejecting escapes.
func projectRoot(path, service string) (string, error) {
	if service == "" {
		return path, nil
	}
	clean := filepath.Clean(service)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("--service must name a subdirectory inside the project, got %q", service)
	}
	return filepath.Join(path, clean), nil
}

// detectPlan runs every registered detector (or the one named by --stack)
// and returns the highest-confidence plan.
func detectPlan(root, stack string) (plan.Plan, error) {
	active := detectors
	if stack != "" {
		active = nil
		for _, d := range detectors {
			if d.Name() == stack {
				active = []detect.Detector{d}
				break
			}
		}
		if active == nil {
			names := make([]string, len(detectors))
			for i, d := range detectors {
				names[i] = d.Name()
			}
			return plan.Plan{}, fmt.Errorf("unknown stack %q (supported: %s)", stack, strings.Join(names, ", "))
		}
	}
	var best plan.Plan
	var firstErr error
	found := false
	for _, d := range active {
		p, ok, err := d.Detect(root)
		if err != nil {
			if stack != "" {
				return plan.Plan{}, fmt.Errorf("detect %s: %w", stack, err)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", d.Name(), err)
			}
			continue
		}
		if !ok {
			continue
		}
		found = true
		if p.Confidence > best.Confidence {
			best = p
		}
	}
	if found {
		return best, nil
	}
	if stack != "" {
		return plan.Plan{}, fmt.Errorf("no %s project detected in %s", stack, root)
	}
	if firstErr != nil {
		return plan.Plan{}, fmt.Errorf("no supported stack detected in %s (first error: %w)", root, firstErr)
	}
	return plan.Plan{}, fmt.Errorf("no supported stack detected in %s — looked for package.json, go.mod, pyproject.toml, requirements.txt, or setup.py", root)
}

// renderAll produces the stack's own artifacts plus the shared ones:
// compose, .env.example when the plan declares variables, the GHCR
// workflow, and DEPLOY.md.
func renderAll(p plan.Plan) ([]emit.File, error) {
	var stack []emit.File
	var err error
	switch p.Stack {
	case "node":
		stack, err = noderender.RenderNode(p)
	case "go":
		stack, err = golangrender.RenderGo(p)
	case "python":
		stack, err = pythonrender.RenderPython(p)
	default:
		return nil, fmt.Errorf("no renderer for stack %q", p.Stack)
	}
	if err != nil {
		return nil, err
	}
	files := append([]emit.File{}, stack...)
	compose, err := common.RenderCompose(p)
	if err != nil {
		return nil, err
	}
	files = append(files, compose)
	if len(p.Env) > 0 {
		files = append(files, emit.EnvExample(p))
	}
	for _, render := range []func(plan.Plan) (emit.File, error){common.RenderAction, common.RenderDeployDoc} {
		f, err := render(p)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// interactive reports whether stdin is a terminal the user can answer on.
func interactive(cmd *cobra.Command) bool {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	info, err := in.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// confirm shows the plan and artifact list, then requires "y" to proceed.
func confirm(cmd *cobra.Command, root string, p plan.Plan, files []emit.File) error {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Plan: %s %s %s", p.Stack, p.Version, p.Process)
	if p.Framework != "" {
		fmt.Fprintf(&prompt, " (%s)", p.Framework)
	}
	if p.Port != 0 {
		fmt.Fprintf(&prompt, " port %d", p.Port)
	}
	if len(p.Services) > 0 {
		names := make([]string, len(p.Services))
		for i, s := range p.Services {
			names[i] = string(s)
		}
		fmt.Fprintf(&prompt, " services: %s", strings.Join(names, ", "))
	}
	prompt.WriteByte('\n')
	for _, n := range p.Notes {
		fmt.Fprintf(&prompt, "  note: %s\n", n)
	}
	for _, f := range files {
		fmt.Fprintf(&prompt, "  %s\n", f.Path)
	}
	fmt.Fprintf(&prompt, "Write these %d files to %s? [y/N] ", len(files), root)
	if _, err := io.WriteString(cmd.ErrOrStderr(), prompt.String()); err != nil {
		return err
	}
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return errors.New("aborted")
	}
	return nil
}

func printReport(cmd *cobra.Command, rep report, asJSON bool) {
	out := cmd.OutOrStdout()
	if asJSON {
		_ = json.NewEncoder(out).Encode(rep)
		return
	}
	var text strings.Builder
	p := rep.Plan
	fmt.Fprintf(&text, "Plan: %s %s %s", p.Stack, p.Version, p.Process)
	if p.Framework != "" {
		fmt.Fprintf(&text, " (%s)", p.Framework)
	}
	if p.Port != 0 {
		fmt.Fprintf(&text, " port %d", p.Port)
	}
	fmt.Fprintf(&text, " confidence %.1f\n", p.Confidence)
	for _, r := range rep.Files {
		fmt.Fprintf(&text, "  %s %s\n", r.Action, r.Path)
	}
	switch {
	case rep.Verify == nil:
		text.WriteString("Verify: skipped\n")
	case rep.Verify.Smoke != nil:
		s := rep.Verify.Smoke
		switch {
		case s.Skipped:
			fmt.Fprintf(&text, "Verify: smoke skipped (%s)\n", s.Reason)
		case s.OK:
			fmt.Fprintf(&text, "Verify: smoke ok (HTTP %d after %d attempts)\n", s.StatusCode, s.Attempts)
		default:
			text.WriteString("Verify: smoke failed\n")
		}
	case rep.Verify.Build != nil:
		b := rep.Verify.Build
		if b.OK {
			fmt.Fprintf(&text, "Verify: build ok (%d ms, %s)\n", b.DurationMs, b.Image)
		} else {
			text.WriteString("Verify: build failed\n")
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(&text, "Note: %s\n", n)
	}
	_, _ = io.WriteString(out, text.String())
}

// printVerifyDiagnostics sends the failed step, log tail, and hint to
// stderr so the machine-readable stdout report stays clean.
func printVerifyDiagnostics(cmd *cobra.Command, v *verifyOutcome) {
	var text strings.Builder
	if v.Build != nil && !v.Build.OK {
		if v.Build.FailedStep != "" {
			fmt.Fprintf(&text, "failed step: %s\n", v.Build.FailedStep)
		}
		if v.Build.Stderr != "" {
			fmt.Fprintf(&text, "%s\n", v.Build.Stderr)
		}
		if v.Build.Hint != "" {
			fmt.Fprintf(&text, "hint: %s\n", v.Build.Hint)
		}
	}
	if v.Smoke != nil && !v.Smoke.OK && !v.Smoke.Skipped {
		if v.Smoke.Logs != "" {
			fmt.Fprintf(&text, "container logs:\n%s\n", v.Smoke.Logs)
		}
		if v.Smoke.Hint != "" {
			fmt.Fprintf(&text, "hint: %s\n", v.Smoke.Hint)
		}
	}
	_, _ = io.WriteString(cmd.ErrOrStderr(), text.String())
}
