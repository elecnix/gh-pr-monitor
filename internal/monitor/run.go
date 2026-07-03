package monitor

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elecnix/gh-pr-monitor/internal/prefs"
	"github.com/elecnix/gh-pr-monitor/internal/resolver"
)

// firstPollType and allClearType are loop-level notification kinds that are not
// Diff events but do have templates in prefs.
const (
	firstPollType = "first-poll"
)

// maxIdleInterval caps the adaptive idle backoff, and maxErrBackoff caps the
// transient-error backoff. Both mirror pi-ghpr-monitor's 5-minute ceilings.
const (
	maxIdleInterval = 300 * time.Second
	maxErrBackoff   = 300 * time.Second
	defaultInterval = 60 * time.Second
)

// Notification is one emitted event, rendered for a consumer. It serializes to a
// single NDJSON line; a persistent watcher (e.g. Claude Code's Monitor tool)
// surfaces each line as a session notification.
type Notification struct {
	Type              string   `json:"type"`
	PRLabel           string   `json:"pr_label"`
	Message           string   `json:"message"`
	UnresolvedThreads int      `json:"unresolved_threads,omitempty"`
	GeneralComments   int      `json:"general_comments,omitempty"`
	FailingChecks     []string `json:"failing_checks,omitempty"`
	CommitShortOid    string   `json:"commit_short_oid,omitempty"`
	CommitAuthor      string   `json:"commit_author,omitempty"`
	ReviewAuthor      string   `json:"review_author,omitempty"`
	// Detail is a rich, self-contained body (thread/comment excerpts + hints) so
	// a consumer can act without extra API calls (new-*-threads/comments only).
	Detail string `json:"detail,omitempty"`
	// PRUrl and CommitUrl back the OSC-8 links in --text output.
	PRUrl     string    `json:"pr_url,omitempty"`
	CommitUrl string    `json:"commit_url,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// RunOptions configures a monitor run.
type RunOptions struct {
	Identity resolver.Identity
	Prefs    prefs.Preferences
	Interval time.Duration
	// Timeout stops the loop after this duration; 0 means run forever.
	Timeout time.Duration

	// Now and Sleep are injectable for tests. Now defaults to time.Now; Sleep
	// defaults to a context-aware timer. Sleep must return the context error
	// when the context is cancelled.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}

func (o *RunOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *RunOptions) sleep(ctx context.Context, d time.Duration) error {
	if o.Sleep != nil {
		return o.Sleep(ctx, d)
	}
	return realSleep(ctx, d)
}

func realSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run polls the PR until it is merged/closed, the context is cancelled, or the
// timeout elapses, emitting one Notification per genuinely-new change.
//
// The first poll emits an informational first-poll notification and establishes
// a silent baseline (no historical spam). Subsequent polls diff against the
// prior snapshot. After 3 consecutive no-change polls the interval grows
// exponentially (capped at 5 minutes) and resets to the base interval on any
// change. Transient fetch errors are logged to stderr and retried with a
// separate doubling backoff rather than crashing the loop.
func Run(ctx context.Context, svc *Service, opts RunOptions, emit func(Notification)) error {
	base := opts.Interval
	if base <= 0 {
		base = defaultInterval
	}

	var deadline time.Time
	if opts.Timeout > 0 {
		deadline = opts.now().Add(opts.Timeout)
	}

	// diff selects the per-poll change detector. With RetriggerComments the loop
	// re-emits every open thread/comment on each poll (via DiffRetrigger); since
	// an open item keeps changes flowing, noChange rarely accrues and the idle
	// backoff is effectively disabled — pair it with a longer --interval.
	diff := Diff
	if opts.Prefs.RetriggerComments {
		diff = DiffRetrigger
	}

	var prev *PRStatus
	noChange := 0
	errBackoff := time.Duration(0)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		resp, err := svc.Fetch(&opts.Identity, opts.Identity.Number)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gh-pr-monitor: fetch error: %v\n", err)
			errBackoff = nextErrBackoff(errBackoff, base)
			if serr := opts.sleep(ctx, errBackoff); serr != nil {
				return serr
			}
			continue
		}
		errBackoff = 0

		curr := Snapshot(resp.Repository.PullRequest, SnapshotOptions{IgnoredBots: opts.Prefs.IgnoredBots})

		firstPoll := prev == nil
		terminalEmitted := false
		if firstPoll {
			emit(renderNotification(opts, curr, firstPollType, Event{}))
		} else {
			events := diff(prev, curr)
			for _, ev := range events {
				emit(renderNotification(opts, curr, string(ev.Type), ev))
				if ev.Type == EventMerged || ev.Type == EventClosed {
					terminalEmitted = true
				}
			}
			if len(events) == 0 {
				noChange++
			} else {
				noChange = 0
			}
		}
		prev = curr

		// Auto-stop when the PR reaches a terminal state. On a transition Diff
		// already emitted the terminal event; if the PR was already terminal at
		// startup, emit it now so a consumer always learns why the stream ends.
		if curr.Merged || curr.State == "CLOSED" {
			if firstPoll && !terminalEmitted {
				typ := EventClosed
				if curr.Merged {
					typ = EventMerged
				}
				emit(renderNotification(opts, curr, string(typ), Event{Type: typ}))
			}
			return nil
		}

		d := idleInterval(base, noChange)
		if !deadline.IsZero() {
			remaining := deadline.Sub(opts.now())
			if remaining <= 0 {
				return nil
			}
			if d > remaining {
				d = remaining
			}
		}
		if err := opts.sleep(ctx, d); err != nil {
			return err
		}
	}
}

// Once does a single fetch and emits the current actionable state: an
// informational first-poll notification followed by one notification per
// currently-actionable item (diffing against an empty baseline). It never
// blocks. Useful for inspection or a one-shot check.
func Once(ctx context.Context, svc *Service, opts RunOptions, emit func(Notification)) error {
	resp, err := svc.Fetch(&opts.Identity, opts.Identity.Number)
	if err != nil {
		return err
	}
	curr := Snapshot(resp.Repository.PullRequest, SnapshotOptions{IgnoredBots: opts.Prefs.IgnoredBots})
	emit(renderNotification(opts, curr, firstPollType, Event{}))
	for _, ev := range Diff(&PRStatus{}, curr) {
		emit(renderNotification(opts, curr, string(ev.Type), ev))
	}
	return nil
}

// idleInterval returns the poll interval given the number of consecutive
// no-change polls: base until 3 no-change polls, then base*2^(n-3) capped at
// maxIdleInterval.
func idleInterval(base time.Duration, noChange int) time.Duration {
	d := base
	if noChange >= 3 {
		shift := uint(noChange - 3)
		if shift > 20 {
			shift = 20
		}
		d = base * time.Duration(uint64(1)<<shift)
	}
	if d > maxIdleInterval {
		d = maxIdleInterval
	}
	if d < base {
		d = base
	}
	return d
}

// nextErrBackoff doubles the current error backoff (starting at base), capped.
func nextErrBackoff(cur, base time.Duration) time.Duration {
	if cur <= 0 {
		if base > maxErrBackoff {
			return maxErrBackoff
		}
		return base
	}
	d := cur * 2
	if d > maxErrBackoff {
		d = maxErrBackoff
	}
	return d
}

// renderNotification builds the interpolation vars, renders the template for
// typ, and populates the structured fields relevant to the event.
func renderNotification(opts RunOptions, status *PRStatus, typ string, ev Event) Notification {
	vars := buildVars(opts.Identity, status, ev, opts.Interval)
	n := Notification{
		Type:      typ,
		PRLabel:   vars["prLabel"],
		Message:   prefs.Interpolate(opts.Prefs.Templates[typ], vars),
		Timestamp: opts.now(),
	}
	n.PRUrl = vars["prUrl"]
	if status != nil {
		n.UnresolvedThreads = len(status.UnresolvedThreads)
		n.GeneralComments = len(status.GeneralComments)
	}
	switch EventType(typ) {
	case EventNewFailingChecks:
		if len(ev.Checks) > 0 {
			n.FailingChecks = ev.Checks
		} else if status != nil {
			n.FailingChecks = status.FailingChecks
		}
	case EventNewUnresolvedThreads:
		n.Detail = threadsDetail(ev.Threads)
	case EventNewGeneralComments:
		n.Detail = commentsDetail(ev.Comments)
	case EventNewCommit:
		if ev.Commit != nil {
			n.CommitShortOid = ev.Commit.ShortOid
			n.CommitAuthor = ev.Commit.Author
			n.CommitUrl = vars["commitUrl"]
		}
	case EventReviewApproved, EventReviewChangesRequested, EventReviewDismissed:
		n.ReviewAuthor = ev.ReviewAuthor
	}
	return n
}

// buildVars assembles the interpolation variables from the identity, snapshot,
// and event. Event-specific fields override the snapshot-derived defaults.
func buildVars(id resolver.Identity, status *PRStatus, ev Event, interval time.Duration) map[string]string {
	host := id.Host
	if host == "" {
		host = "github.com"
	}
	vars := map[string]string{
		"owner":       id.Owner,
		"repo":        id.Repo,
		"number":      strconv.Itoa(id.Number),
		"host":        host,
		"prLabel":     fmt.Sprintf("%s/%s#%d", id.Owner, id.Repo, id.Number),
		"prUrl":       fmt.Sprintf("https://%s/%s/%s/pull/%d", host, id.Owner, id.Repo, id.Number),
		"intervalSec": strconv.Itoa(int(interval.Seconds())),
	}

	if status != nil {
		vars["unresolvedThreads"] = strconv.Itoa(len(status.UnresolvedThreads))
		vars["generalComments"] = strconv.Itoa(len(status.GeneralComments))
		vars["failingChecks"] = strings.Join(status.FailingChecks, ", ")
		vars["conflict"] = strconv.FormatBool(status.Conflict)
		vars["reviewAuthor"] = status.ReviewAuthor
		setCommitVars(vars, host, id, status.LastCommit)
	}

	if ev.Type == EventNewFailingChecks && len(ev.Checks) > 0 {
		vars["failingChecks"] = strings.Join(ev.Checks, ", ")
	}
	if ev.ReviewAuthor != "" {
		vars["reviewAuthor"] = ev.ReviewAuthor
	}
	if ev.Commit != nil {
		setCommitVars(vars, host, id, *ev.Commit)
	}

	return vars
}

func setCommitVars(vars map[string]string, host string, id resolver.Identity, c CommitSummary) {
	vars["commitOid"] = c.Oid
	vars["commitShortOid"] = c.ShortOid
	vars["commitAuthor"] = c.Author
	vars["commitCoauthors"] = strings.Join(c.Coauthors, ", ")
	vars["commitMessageHeadline"] = c.MessageHeadline
	if c.Oid != "" {
		vars["commitUrl"] = fmt.Sprintf("https://%s/%s/%s/commit/%s", host, id.Owner, id.Repo, c.Oid)
	}
}
