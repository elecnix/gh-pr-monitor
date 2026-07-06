package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/elecnix/gh-pr-monitor/internal/monitor"
	"github.com/elecnix/gh-pr-monitor/internal/prefs"
	"github.com/elecnix/gh-pr-monitor/internal/resolver"
)

func newMonitorCommand() *cobra.Command {
	opts := &monitorOptions{}

	cmd := &cobra.Command{
		Use:     "monitor <number> | <url>",
		Aliases: []string{"watch"},
		Short:   "Continuously watch a pull request and stream events as they happen",
		Long: `Continuously watch a pull request, emitting one event per genuinely-new
change: new review threads, general comments, failing/green CI, merge
conflicts, review decisions, new commits, and merge/close.

By default each event is printed as one NDJSON line on stdout, so a persistent
watcher (such as Claude Code's Monitor tool wrapping this command) surfaces each
line as a notification. Use --text to print only the rendered message per line.

Notification wording is templated and user-overridable via the preferences file
at ${XDG_CONFIG_HOME:-~/.config}/gh-pr-monitor/preferences.json.

The loop auto-stops when the PR is merged or closed. Idle polling backs off
exponentially (capped at 5 minutes) and resets on any change.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Selector = args[0]
			}
			return runMonitor(cmd, opts)
		},
	}

	addMonitorFlags(cmd, opts)

	return cmd
}

func addMonitorFlags(cmd *cobra.Command, opts *monitorOptions) {
	cmd.Flags().StringVarP(&opts.Repo, "repo", "R", "", "Repository in 'owner/repo' format")
	cmd.Flags().IntVar(&opts.Pull, "pr", 0, "Pull request number")
	cmd.Flags().StringVar(&opts.Ref, "ref", "", "Branch or ref to monitor (CI checks only)")
	cmd.Flags().StringVar(&opts.Commit, "commit", "", "Commit SHA to monitor (CI checks only)")
	cmd.Flags().IntVar(&opts.Issue, "issue", 0, "Issue number to monitor")
	cmd.Flags().IntVarP(&opts.Interval, "interval", "i", 60, "Polling interval in seconds (min 10)")
	cmd.Flags().IntVarP(&opts.Timeout, "timeout", "t", 0, "Maximum watch time in seconds (0 = run until merged/closed)")
	cmd.Flags().StringVar(&opts.IgnoredBots, "ignored-bots", "", "Comma-separated author logins whose general comments are ignored")
	cmd.Flags().BoolVar(&opts.Once, "once", false, "Fetch once, emit the current actionable state, and exit")
	cmd.Flags().BoolVar(&opts.Text, "text", false, "Emit the rendered message per event instead of NDJSON")
}

type monitorOptions struct {
	Repo        string
	Pull        int
	Ref         string
	Commit      string
	Issue       int
	Selector    string
	Interval    int
	Timeout     int
	IgnoredBots string
	Once        bool
	Text        bool
}

func (o *monitorOptions) Validate() error {
	if o.Interval < 10 {
		return errors.New("--interval must be at least 10 seconds")
	}
	if o.Timeout < 0 {
		return errors.New("--timeout must be a non-negative integer")
	}

	// Count how many target kinds are specified.
	targets := 0
	if o.Selector != "" || o.Pull > 0 {
		targets++
	}
	if o.Ref != "" {
		targets++
	}
	if o.Commit != "" {
		targets++
	}
	if o.Issue > 0 {
		targets++
	}
	if targets > 1 {
		return errors.New("--ref, --commit, --issue, and a PR selector are mutually exclusive")
	}
	if targets == 0 {
		return errors.New("pull request number or URL is required")
	}

	return nil
}

func runMonitor(cmd *cobra.Command, opts *monitorOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}

	inferRepo(&opts.Repo)

	var identity resolver.Identity
	var err error

	if opts.Ref != "" {
		identity, err = resolver.ResolveRef(opts.Ref, opts.Repo, os.Getenv("GH_HOST"))
	} else if opts.Commit != "" {
		identity, err = resolver.ResolveCommit(opts.Commit, opts.Repo, os.Getenv("GH_HOST"))
	} else if opts.Issue > 0 {
		identity, err = resolver.ResolveIssue(opts.Issue, opts.Repo, os.Getenv("GH_HOST"))
	} else {
		inferPR(opts.Selector, &opts.Pull)
		selector, normErr := resolver.NormalizeSelector(opts.Selector, opts.Pull)
		if normErr != nil {
			return normErr
		}
		identity, err = resolver.Resolve(selector, opts.Repo, os.Getenv("GH_HOST"))
	}
	if err != nil {
		return err
	}

	p, err := prefs.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gh-pr-monitor: using default templates (%v)\n", err)
	}
	for _, bot := range strings.Split(opts.IgnoredBots, ",") {
		if b := strings.TrimSpace(bot); b != "" {
			p.IgnoredBots = append(p.IgnoredBots, b)
		}
	}

	svc := &monitor.Service{API: apiClientFactory(identity.Host)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	runOpts := monitor.RunOptions{
		Identity: identity,
		Prefs:    p,
		Interval: time.Duration(opts.Interval) * time.Second,
		Timeout:  time.Duration(opts.Timeout) * time.Second,
	}

	emit := func(n monitor.Notification) {
		if opts.Text {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, monitor.LinkifyText(n))
			if n.Detail != "" {
				for _, line := range strings.Split(n.Detail, "\n") {
					fmt.Fprintf(out, "  %s\n", line)
				}
			}
			return
		}
		if err := encodeJSON(cmd, n); err != nil {
			fmt.Fprintf(os.Stderr, "gh-pr-monitor: %v\n", err)
		}
	}

	if opts.Once {
		return monitor.Once(ctx, svc, runOpts, emit)
	}

	if err := monitor.Run(ctx, svc, runOpts, emit); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil // clean shutdown on signal
		}
		return err
	}
	return nil
}
