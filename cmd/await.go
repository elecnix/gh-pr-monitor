package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/elecnix/gh-pr-monitor/internal/await"
	"github.com/elecnix/gh-pr-monitor/internal/resolver"
)

func newAwaitCommand() *cobra.Command {
	opts := &awaitOptions{}

	cmd := &cobra.Command{
		Use:   "await <number> | <url>",
		Short: "Poll a pull request until it needs attention",
		Long: `Poll a pull request until review comments, merge conflicts, or CI failures appear.

Exit codes:
  0  Work detected — PR needs attention
  1  Error occurred
  2  Timed out with no work detected`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Selector = args[0]
			}
			return runAwait(cmd, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Repo, "repo", "R", "", "Repository in 'owner/repo' format")
	cmd.Flags().IntVar(&opts.Pull, "pr", 0, "Pull request number")
	cmd.Flags().StringVar(&opts.Mode, "mode", "all", "Watch mode: all, comments, conflicts, actions")
	cmd.Flags().IntVarP(&opts.Timeout, "timeout", "t", 86400, "Maximum polling time in seconds (default: 86400 = 1 day)")
	cmd.Flags().IntVarP(&opts.Interval, "interval", "i", 300, "Polling interval in seconds (default: 300 = 5 minutes)")
	cmd.Flags().IntVar(&opts.Debounce, "debounce", 30, "Debounce duration in seconds (default: 30)")
	cmd.Flags().BoolVarP(&opts.CheckOnly, "check-only", "c", false, "Check once and exit (no polling)")

	return cmd
}

type awaitOptions struct {
	Repo      string
	Pull      int
	Selector  string
	Mode      string
	Timeout   int
	Interval  int
	Debounce  int
	CheckOnly bool
}

func runAwait(cmd *cobra.Command, opts *awaitOptions) error {
	// Validate
	if err := opts.Validate(); err != nil {
		return err
	}

	// Resolve selector
	selector, err := resolver.NormalizeSelector(opts.Selector, opts.Pull)
	if err != nil {
		return err
	}

	// Get identity
	hostEnv := os.Getenv("GH_HOST")
	identity, err := resolver.Resolve(selector, opts.Repo, hostEnv)
	if err != nil {
		return err
	}

	// Parse mode
	mode, err := await.ParseMode(opts.Mode)
	if err != nil {
		return err
	}

	// Create service
	service := &await.Service{API: apiClientFactory(identity.Host)}

	// Create context with cancellation for signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Build watch options
	watchOpts := await.WatchOptions{
		Interval: time.Duration(opts.Interval) * time.Second,
		Debounce: time.Duration(opts.Debounce) * time.Second,
		Timeout:  time.Duration(opts.Timeout) * time.Second,
		Mode:     mode,
	}

	// Check-only mode: single fetch without polling
	if opts.CheckOnly {
		return runCheckOnly(cmd, service, &identity, mode)
	}

	// Run watch with polling
	result, err := service.Watch(ctx, &identity, watchOpts)
	if err != nil {
		return err
	}

	// Output JSON result
	if err := encodeJSON(cmd, result); err != nil {
		return err
	}

	// Exit with code 2 if timed out with no work
	if result.TimedOut && len(result.Conditions) == 0 {
		os.Exit(2)
	}

	return nil
}

func runCheckOnly(cmd *cobra.Command, service *await.Service, identity *resolver.Identity, mode await.Mode) error {
	data, err := service.Fetch(identity, identity.Number)
	if err != nil {
		return err
	}

	pr := data.Repository.PullRequest
	conditions := await.Conditions(pr, mode)

	result := &await.Result{
		Conditions:  conditions,
		Unresolved:  await.CountUnresolvedThreads(pr),
		General:     len(pr.Comments.Nodes),
		Conflicts:   await.HasConflicts(pr),
		Failing:     await.FailingChecks(pr),
		Pending:     await.PendingChecks(pr),
		Annotations: await.FailingAnnotations(pr),
		Threads:     await.ThreadSummaries(pr),
		TimedOut:    false,
		Cancelled:   false,
		WatchedMs:   0,
	}

	if err := encodeJSON(cmd, result); err != nil {
		return err
	}

	// Exit with code 2 if no work detected
	if len(conditions) == 0 {
		os.Exit(2)
	}

	return nil
}

func (o *awaitOptions) Validate() error {
	if o.Timeout < 0 {
		return errors.New("--timeout must be a non-negative integer")
	}
	if o.Interval <= 0 {
		return errors.New("--interval must be a positive integer (> 0)")
	}
	if o.Debounce <= 0 {
		return errors.New("--debounce must be a positive integer (> 0)")
	}
	if o.Selector == "" && o.Pull == 0 {
		return errors.New("pull request number or URL is required")
	}
	return nil
}
