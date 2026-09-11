package main

import (
	"encoding/json"
	"fmt"
	"os"

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
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:           "dockerizethis [path]",
		Short:         "Generate verified Docker hosting artifacts for a project",
		Long:          "Generate verified Docker hosting artifacts for a project.\nThe path defaults to \".\". This scaffold does not generate or verify files yet.",
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

// run reserves the pipeline order until the internal packages are implemented.
func run(cmd *cobra.Command, path string, opts options) error {
	// TODO(detect): call internal/detect for path and opts.service, respecting opts.stack.
	// TODO(plan): validate and complete the detected internal/plan.Plan; use opts.yes for prompts.
	// TODO(emit): render artifacts and call internal/emit.Write with dry-run, force, and backup options.
	// TODO(verify): call internal verification for opts.verify; skip execution during dry-run.
	// TODO(report): call internal reporting with the emission and verification results.
	message := "Pipeline not implemented; no files generated or verified."
	if opts.json {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		}{Path: path, Message: message})
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\nProject: %s\n", message, path)
	return err
}
