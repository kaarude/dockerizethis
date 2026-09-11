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
	"slices"
	"strings"
	"syscall"

	"github.com/carl/dockerizethis/internal/detect"
	"github.com/carl/dockerizethis/internal/detect/golang"
	"github.com/carl/dockerizethis/internal/detect/node"
	"github.com/carl/dockerizethis/internal/detect/python"
	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/carl/dockerizethis/internal/templates/common"
	gotemplate "github.com/carl/dockerizethis/internal/templates/golang"
	nodetemplate "github.com/carl/dockerizethis/internal/templates/node"
	pytemplate "github.com/carl/dockerizethis/internal/templates/python"
	"github.com/carl/dockerizethis/internal/verify"
	"github.com/spf13/cobra"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCommand().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(verify.Code(err))
	}
}

func newRootCommand() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "dockerizethis [path]",
		Short: "Generate verified Docker hosting artifacts for a project",
		Long:  "Detect a Node.js, Go, or Python project and generate Docker hosting artifacts.\nThe path defaults to \".\". Existing artifacts are kept unless --force or --backup is set.\nVerification builds the image by default; --dry-run never writes or runs Docker.",
		Args:  cobra.MaximumNArgs(1), SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := verify.ParseMode(opts.verify)
			if err != nil {
				return err
			}
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			return run(cmd, path, opts, mode)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.dryRun, "dry-run", false, "Preview artifacts without writing files")
	flags.BoolVar(&opts.yes, "yes", false, "Accept prompts without interaction")
	flags.BoolVar(&opts.json, "json", false, "Print the report as JSON")
	flags.StringVar(&opts.verify, "verify", "build", "Verification level: none, build, or full")
	flags.StringVar(&opts.stack, "stack", "", "Select a detected stack: node, go, or python")
	flags.StringVar(&opts.service, "service", "", "Select a service subdirectory in a monorepo")
	flags.BoolVar(&opts.force, "force", false, "Allow replacing existing artifact files")
	flags.BoolVar(&opts.backup, "backup", false, "Back up existing artifact files before replacement")
	return cmd
}

type report struct {
	Plan    *plan.Plan    `json:"plan"`
	Results []emit.Result `json:"results"`
	Verify  verify.Report `json:"verify"`
}

func run(cmd *cobra.Command, path string, opts options, mode verify.Mode) (runErr error) {
	r := report{Results: []emit.Result{}, Verify: verify.Report{Mode: mode, Status: "skipped", Reason: "artifacts not generated"}}
	defer func() {
		if runErr != nil {
			r.Verify.Error = runErr.Error()
		}
		if opts.json {
			runErr = errors.Join(runErr, json.NewEncoder(cmd.OutOrStdout()).Encode(r))
		} else {
			runErr = errors.Join(runErr, printReport(cmd.OutOrStdout(), path, r, opts.dryRun))
		}
	}()
	if opts.service != "" {
		if !filepath.IsLocal(opts.service) {
			return fmt.Errorf("--service must be a subdirectory within the project")
		}
		// Compare resolved paths so a service symlink cannot select a different project.
		projectRoot, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		projectRoot, err = filepath.EvalSymlinks(projectRoot)
		if err != nil {
			return fmt.Errorf("resolve project: %w", err)
		}
		selected, err := filepath.EvalSymlinks(filepath.Join(projectRoot, opts.service))
		if err != nil {
			return fmt.Errorf("resolve service: %w", err)
		}
		rel, err := filepath.Rel(projectRoot, selected)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("--service must be a subdirectory within the project")
		}
		path = selected
	}
	var err error
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect project: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project path must be a directory: %s", path)
	}
	p, err := detectPlan(path, opts.stack, []detect.Detector{node.Detector{}, golang.Detector{}, python.Detector{}})
	if err != nil {
		return err
	}
	p = completePlan(p)
	if opts.service != "" {
		p.Notes = append(p.Notes, "move the generated workflow to the repository root .github/workflows directory and set its build context to "+filepath.ToSlash(opts.service))
	}
	r.Plan = &p
	files, err := render(p)
	if err != nil {
		return err
	}
	if !opts.yes && !opts.dryRun && isTerminal(cmd.OutOrStdout()) {
		accepted, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(), files)
		if err != nil {
			return err
		}
		if !accepted {
			r.Verify.Reason = "cancelled; no files written"
			return nil
		}
	}
	r.Results, err = emit.Write(path, files, emit.Options{DryRun: opts.dryRun, Force: opts.force, Backup: opts.backup})
	if err != nil {
		return err
	}
	if opts.dryRun {
		r.Verify.Reason = "dry-run"
		return nil
	}
	r.Verify, err = verify.Run(cmd.Context(), path, p, verify.Options{Mode: mode, Progress: cmd.ErrOrStderr()})
	return err
}

// Ties keep detector order, so selection is stable across runs.
func detectPlan(dir, stack string, detectors []detect.Detector) (plan.Plan, error) {
	type match struct {
		name string
		plan plan.Plan
	}
	var matches []match
	var failures []error
	var checked []string
	for _, d := range detectors {
		checked = append(checked, d.Name())
		p, ok, err := d.Detect(dir)
		if err != nil {
			failures = append(failures, fmt.Errorf("detect %s: %w", d.Name(), err))
			continue
		}
		if ok {
			matches = append(matches, match{d.Name(), p})
		}
	}
	if len(failures) > 0 {
		return plan.Plan{}, errors.Join(failures...)
	}
	if stack != "" {
		for _, m := range matches {
			if m.name == stack {
				return m.plan, nil
			}
		}
		return plan.Plan{}, fmt.Errorf("stack %q was not detected; checked %s", stack, strings.Join(checked, ", "))
	}
	if len(matches) == 0 {
		return plan.Plan{}, fmt.Errorf("no supported project detected; checked %s: package.json, go.mod, pyproject.toml, requirements.txt, setup.py", strings.Join(checked, ", "))
	}
	best := matches[0].plan
	for _, m := range matches[1:] {
		if m.plan.Confidence > best.Confidence {
			best = m.plan
		}
	}
	return best, nil
}

// Compose has configuration of its own, in addition to the app's detected env.
func completePlan(p plan.Plan) plan.Plan {
	p.Env = slices.Clone(p.Env)
	add := func(name, hint string) {
		for i := range p.Env {
			if p.Env[i].Name == name {
				p.Env[i].Required = true
				if p.Env[i].Hint == "" {
					p.Env[i].Hint = hint
				}
				return
			}
		}
		p.Env = append(p.Env, plan.EnvVar{Name: name, Required: true, Hint: hint})
	}
	for _, service := range p.Services {
		switch service {
		case plan.ServicePostgres:
			add("POSTGRES_PASSWORD", "Password for the Compose postgres service; use the same password in DATABASE_URL with host postgres and port 5432")
		case plan.ServiceMySQL:
			add("MYSQL_PASSWORD", "Password for the Compose mysql app user")
			add("MYSQL_ROOT_PASSWORD", "Root password for the Compose mysql service")
		case plan.ServiceMongo:
			add("MONGO_INITDB_ROOT_PASSWORD", "Root password for the Compose mongo service")
		}
	}
	slices.SortFunc(p.Env, func(a, b plan.EnvVar) int { return strings.Compare(a.Name, b.Name) })
	if p.Stack == "node" {
		p.Notes = append(p.Notes, nodetemplate.RenderNotes(p)...)
	}
	return p
}

func render(p plan.Plan) ([]emit.File, error) {
	var files []emit.File
	var err error
	switch p.Stack {
	case "node":
		files, err = nodetemplate.RenderNode(p)
	case "go":
		files, err = gotemplate.RenderGo(p)
	case "python":
		files, err = pytemplate.RenderPython(p)
	default:
		return nil, fmt.Errorf("no renderer for stack %q", p.Stack)
	}
	if err != nil {
		return nil, err
	}
	compose, err := common.RenderCompose(p)
	if err != nil {
		return nil, err
	}
	files = append(files, compose)
	if len(p.Env) > 0 {
		files = append(files, emit.EnvExample(p))
	}
	for _, render := range []func(plan.Plan) (emit.File, error){common.RenderAction, common.RenderDeployDoc} {
		file, err := render(p)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func confirm(in io.Reader, out io.Writer, files []emit.File) (bool, error) {
	var prompt strings.Builder
	prompt.WriteString("Artifacts to write:\n")
	for _, f := range files {
		fmt.Fprintf(&prompt, "  %s\n", f.Path)
	}
	prompt.WriteString("Write these files? [y/N] ")
	if _, err := io.WriteString(out, prompt.String()); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func printReport(out io.Writer, path string, r report, dryRun bool) error {
	var text strings.Builder
	if r.Plan == nil {
		return nil
	}
	p := r.Plan
	framework := p.Framework
	if framework == "" {
		framework = "none"
	}
	services := make([]string, len(p.Services))
	for i, s := range p.Services {
		services[i] = string(s)
	}
	serviceText := strings.Join(services, ", ")
	if serviceText == "" {
		serviceText = "none"
	}
	fmt.Fprintf(&text, "Project: %s\nDetected: %s %s | framework: %s | process: %s\nPort: %d | services: %s | confidence: %.0f%%\n", path, p.Stack, p.Version, framework, p.Process, p.Port, serviceText, p.Confidence*100)
	for _, note := range p.Notes {
		fmt.Fprintf(&text, "Note: %s\n", note)
	}
	for _, result := range r.Results {
		fmt.Fprintf(&text, "  %s %s\n", result.Action, result.Path)
	}
	fmt.Fprintf(&text, "Verification: %s", r.Verify.Status)
	if r.Verify.Reason != "" {
		fmt.Fprintf(&text, " (%s)", r.Verify.Reason)
	}
	text.WriteByte('\n')
	if r.Verify.Build != nil && r.Verify.Build.Image != "" {
		fmt.Fprintf(&text, "Image: %s\n", r.Verify.Build.Image)
	}
	if r.Verify.Build != nil {
		if r.Verify.Build.FailedStep != "" {
			fmt.Fprintf(&text, "Failed build step: %s\n", r.Verify.Build.FailedStep)
		}
		if r.Verify.Build.Hint != "" {
			fmt.Fprintf(&text, "Hint: %s\n", r.Verify.Build.Hint)
		}
	}
	if r.Verify.Smoke != nil && r.Verify.Smoke.URL != "" {
		fmt.Fprintf(&text, "HTTP: %s returned %d\n", r.Verify.Smoke.URL, r.Verify.Smoke.StatusCode)
	}
	if len(r.Results) > 0 {
		if len(p.Env) > 0 {
			text.WriteString("Configure .env from .env.example before starting:\n")
			for _, v := range p.Env {
				label := "optional; omit to keep the app default"
				if v.Required {
					label = "required"
				}
				fmt.Fprintf(&text, "  %s (%s)\n", v.Name, label)
			}
		}
		if dryRun {
			text.WriteString("Next: rerun without --dry-run to write these artifacts.\n")
		} else {
			fmt.Fprintf(&text, "Next: in %s, review DEPLOY.md and run docker compose up -d --build.\n", path)
		}
	}
	_, err := io.WriteString(out, text.String())
	return err
}
