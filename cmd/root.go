package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Execute sets up the root command tree and executes it.
func Execute() error {
	root := newRootCommand()
	return root.Execute()
}

func newRootCommand() *cobra.Command {
	monitorOpts := &monitorOptions{}

	cmd := &cobra.Command{
		Use:           "gh-monitor [<number> | <url>]",
		Short:         "PR review helper commands for gh",
		Long:          "Default command: continuously watch a pull request, emitting one event per genuinely-new change.\n\nRun 'gh monitor --help' for subcommands.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				monitorOpts.Selector = args[0]
			}
			return runMonitor(cmd, monitorOpts)
		},
	}

	addMonitorFlags(cmd, monitorOpts)

	cmd.AddCommand(newCommentsCommand())
	cmd.AddCommand(newDraftCommand())
	cmd.AddCommand(newReviewCommand())
	cmd.AddCommand(newThreadsCommand())

	cmd.AddCommand(newReactCommand())
	return cmd
}

// ExecuteOrExit runs the command tree and exits with a non-zero status on error.
func ExecuteOrExit() {
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
