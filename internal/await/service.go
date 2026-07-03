package await

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elecnix/gh-pr-monitor/internal/ghcli"
	"github.com/elecnix/gh-pr-monitor/internal/resolver"
)

// Service handles PR polling operations.
type Service struct {
	API ghcli.API
}

// AWAIT_QUERY fetches PR data needed for polling with pagination.
const AWAIT_QUERY = `query AwaitPR(
  $owner: String!,
  $repo: String!,
  $number: Int!,
  $firstComments: Int!,
  $firstThreads: Int!,
  $firstReviewComments: Int!,
  $firstCheckSuites: Int!,
  $firstChecks: Int!
) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      comments: comments(first: $firstComments) {
        nodes { id body author { login } createdAt }
        pageInfo { hasNextPage endCursor }
      }
      reviewThreads: reviewThreads(first: $firstThreads) {
        nodes {
          id
          isResolved
          isOutdated
          path
          line
          comments(first: $firstReviewComments) {
            nodes { id body author { login } createdAt }
            pageInfo { hasNextPage endCursor }
          }
        }
        pageInfo { hasNextPage endCursor }
      }
      mergeable
      mergeStateStatus
      commits(last: 1) {
        nodes {
          commit {
            checkSuites: checkSuites(first: $firstCheckSuites) {
              nodes {
                id
                conclusion
                status
                app { name slug }
                checkRuns(first: $firstChecks) {
                  nodes {
                    name
                    conclusion
                    status
                    annotations(first: 5) {
                      nodes {
                        annotationLevel
                        message
                        path
                        location {
                          start { line column }
                          end { line column }
                        }
                        title
                      }
                      totalCount
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

// QueryResponse represents the GraphQL response structure.
type QueryResponse struct {
	Repository struct {
		PullRequest *PullRequest `json:"pullRequest"`
	} `json:"repository"`
}

// PullRequest contains PR data for polling.
type PullRequest struct {
	Comments      CommentNodes `json:"comments"`
	ReviewThreads ThreadNodes  `json:"reviewThreads"`
	Mergeable     string       `json:"mergeable"`
	MergeState    string       `json:"mergeStateStatus"`
	Commits       CommitNodes  `json:"commits"`
}

type CommentNodes struct {
	Nodes    []Comment `json:"nodes"`
	PageInfo PageInfo  `json:"pageInfo"`
}

type PageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type Comment struct {
	ID     string `json:"id"`
	Body   string `json:"body"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	CreatedAt string `json:"createdAt"`
}

type ThreadNodes struct {
	Nodes    []ReviewThread `json:"nodes"`
	PageInfo PageInfo       `json:"pageInfo"`
}

type ReviewThread struct {
	ID         string         `json:"id"`
	IsResolved bool           `json:"isResolved"`
	IsOutdated bool           `json:"isOutdated"`
	Path       string         `json:"path"`
	Line       *int           `json:"line"`
	Comments   ReviewComments `json:"comments"`
}

type ReviewComments struct {
	Nodes    []Comment `json:"nodes"`
	PageInfo PageInfo  `json:"pageInfo"`
}

type CommitNodes struct {
	Nodes []Commit `json:"nodes"`
}

type Commit struct {
	Commit CommitDetails `json:"commit"`
}

type CommitDetails struct {
	CheckSuites SuiteNodes `json:"checkSuites"`
}

type SuiteNodes struct {
	Nodes []CheckSuite `json:"nodes"`
}

type CheckSuite struct {
	ID         string   `json:"id"`
	Conclusion string   `json:"conclusion"`
	Status     string   `json:"status"`
	App        AppInfo  `json:"app"`
	CheckRuns  RunNodes `json:"checkRuns"`
}

type AppInfo struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type RunNodes struct {
	Nodes []CheckRun `json:"nodes"`
}

type CheckRun struct {
	Name        string          `json:"name"`
	Conclusion  string          `json:"conclusion"`
	Status      string          `json:"status"`
	Annotations AnnotationNodes `json:"annotations"`
}

// AnnotationPosition represents a position (line/column) in an annotation location.
type AnnotationPosition struct {
	Line   *int `json:"line"`
	Column *int `json:"column"`
}

// AnnotationLocation represents the start/end span of a check annotation.
type AnnotationLocation struct {
	Start AnnotationPosition `json:"start"`
	End   AnnotationPosition `json:"end"`
}

// CheckAnnotation represents a single annotation on a check run.
type CheckAnnotation struct {
	AnnotationLevel string             `json:"annotationLevel"`
	Message         string             `json:"message"`
	Path            string             `json:"path"`
	Location        AnnotationLocation `json:"location"`
	Title           string             `json:"title"`
}

// StartLine returns the start line or nil for backward compatibility.
func (a *CheckAnnotation) StartLine() *int {
	return a.Location.Start.Line
}

// EndLine returns the end line or nil for backward compatibility.
func (a *CheckAnnotation) EndLine() *int {
	return a.Location.End.Line
}

// AnnotationNodes wraps the annotations connection.
type AnnotationNodes struct {
	Nodes      []CheckAnnotation `json:"nodes"`
	TotalCount int               `json:"totalCount"`
}

// ThreadSummary provides an actionable summary of a review thread,
// giving agents enough context to take action without additional API calls.
type ThreadSummary struct {
	ID         string `json:"id"`
	Path       string `json:"path,omitempty"`
	Line       *int   `json:"line,omitempty"`
	IsResolved bool   `json:"is_resolved"`
	IsOutdated bool   `json:"is_outdated"`
}

// Fetch retrieves PR data for polling.
func (s *Service) Fetch(identity *resolver.Identity, number int) (*QueryResponse, error) {
	var result QueryResponse
	owner := identity.Owner
	repo := identity.Repo
	err := s.API.GraphQL(AWAIT_QUERY, map[string]interface{}{
		"owner":               owner,
		"repo":                repo,
		"number":              number,
		"firstComments":       100,
		"firstThreads":        100,
		"firstReviewComments": 100,
		"firstCheckSuites":    100,
		"firstChecks":         100,
	}, &result)
	if err != nil {
		return nil, err
	}
	if result.Repository.PullRequest == nil {
		return nil, fmt.Errorf("pull request not found or not accessible")
	}
	return &result, nil
}

// CountUnresolvedThreads returns the number of unresolved review threads.
func CountUnresolvedThreads(pr *PullRequest) int {
	count := 0
	for _, t := range pr.ReviewThreads.Nodes {
		if !t.IsResolved {
			count++
		}
	}
	return count
}

// HasConflicts returns true if the PR has merge conflicts.
func HasConflicts(pr *PullRequest) bool {
	return pr.Mergeable == "CONFLICTING"
}

// FailingChecks returns names of failing check suites/runs.
func FailingChecks(pr *PullRequest) []string {
	var failing []string
	for _, commit := range pr.Commits.Nodes {
		for _, suite := range commit.Commit.CheckSuites.Nodes {
			if isFailureConclusion(suite.Conclusion) {
				name := suiteName(&suite)
				failing = append(failing, name)
			}
			for _, run := range suite.CheckRuns.Nodes {
				if isFailureConclusion(run.Conclusion) {
					name := run.Name
					if name == "" {
						name = suiteName(&suite)
					}
					if !contains(failing, name) {
						failing = append(failing, name)
					}
				}
			}
		}
	}
	return failing
}

// PendingChecks returns names of pending/in-progress check suites.
func PendingChecks(pr *PullRequest) []string {
	var pending []string
	for _, commit := range pr.Commits.Nodes {
		for _, suite := range commit.Commit.CheckSuites.Nodes {
			if isPendingStatus(suite.Status) {
				name := suiteName(&suite)
				if !contains(pending, name) {
					pending = append(pending, name)
				}
			}
		}
	}
	return pending
}

func suiteName(s *CheckSuite) string {
	if s.App.Name != "" {
		return s.App.Name
	}
	return s.App.Slug
}

func isFailureConclusion(c string) bool {
	return c == "FAILURE" || c == "ERROR" || c == "TIMED_OUT" || c == "CANCELLED" || c == "ACTION_REQUIRED"
}

func isPendingStatus(s string) bool {
	return s == "IN_PROGRESS" || s == "QUEUED" || s == "WAITING" || s == "STARTUP_FAILURE"
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// Mode represents the polling watch mode.
type Mode string

const (
	ModeAll       Mode = "all"
	ModeComments  Mode = "comments"
	ModeConflicts Mode = "conflicts"
	ModeActions   Mode = "actions"
)

// ParseMode converts a string to Mode.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(s) {
	case "all":
		return ModeAll, nil
	case "comments":
		return ModeComments, nil
	case "conflicts":
		return ModeConflicts, nil
	case "actions":
		return ModeActions, nil
	default:
		return ModeAll, fmt.Errorf("invalid mode: %s (valid: all, comments, conflicts, actions)", s)
	}
}

// Conditions returns conditions that need attention.
func Conditions(pr *PullRequest, mode Mode) []string {
	var conditions []string

	if mode == ModeAll || mode == ModeComments {
		if CountUnresolvedThreads(pr) > 0 {
			conditions = append(conditions, "unresolved-threads")
		}
		if len(pr.Comments.Nodes) > 0 {
			conditions = append(conditions, "new-comments")
		}
	}

	if mode == ModeAll || mode == ModeConflicts {
		if HasConflicts(pr) {
			conditions = append(conditions, "conflicts")
		}
	}

	if mode == ModeAll || mode == ModeActions {
		if len(FailingChecks(pr)) > 0 {
			conditions = append(conditions, "actions:failing")
		}
	}

	return conditions
}

// ThreadSummaries returns actionable summaries of all review threads.
func ThreadSummaries(pr *PullRequest) []ThreadSummary {
	summaries := make([]ThreadSummary, 0, len(pr.ReviewThreads.Nodes))
	for _, t := range pr.ReviewThreads.Nodes {
		summaries = append(summaries, ThreadSummary{
			ID:         t.ID,
			Path:       t.Path,
			Line:       t.Line,
			IsResolved: t.IsResolved,
			IsOutdated: t.IsOutdated,
		})
	}
	return summaries
}

// SecondsToHuman converts seconds to human-readable string.
func SecondsToHuman(seconds int) string {
	if seconds >= 86400 {
		return fmt.Sprintf("%d day(s)", seconds/86400)
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%d hour(s)", seconds/3600)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%d minute(s)", seconds/60)
	}
	return fmt.Sprintf("%d second(s)", seconds)
}

// Now returns current timestamp formatted as HH:MM:SS.
func Now() string {
	return time.Now().Format("15:04:05")
}

// ExitCode represents the script exit code.
type ExitCode int

const (
	ExitWork    ExitCode = 0 // Work detected
	ExitTimeout ExitCode = 2 // Timed out with no work
	ExitError   ExitCode = 1 // Error occurred
)

// FailingAnnotations returns annotations from failing check runs.
// Only annotations from check runs with a failure conclusion are included,
// since those are the actionable ones.
func FailingAnnotations(pr *PullRequest) []CheckAnnotation {
	var annotations []CheckAnnotation
	for _, commit := range pr.Commits.Nodes {
		for _, suite := range commit.Commit.CheckSuites.Nodes {
			for _, run := range suite.CheckRuns.Nodes {
				if isFailureConclusion(run.Conclusion) {
					annotations = append(annotations, run.Annotations.Nodes...)
				}
			}
		}
	}
	return annotations
}

// Result represents the await polling result for JSON output.
// All slice fields are normalized to empty arrays (never null) so that
// downstream JSON consumers (including agent notification injection systems)
// receive consistent, parseable arrays rather than null.
type Result struct {
	Conditions  []string          `json:"conditions"`
	Unresolved  int               `json:"unresolved_threads"`
	General     int               `json:"general_comments"`
	Conflicts   bool              `json:"has_conflicts"`
	Failing     []string          `json:"failing_checks"`
	Pending     []string          `json:"pending_checks"`
	Annotations []CheckAnnotation `json:"annotations,omitempty"`
	Threads     []ThreadSummary   `json:"threads"`
	TimedOut    bool              `json:"timed_out"`
	Cancelled   bool              `json:"cancelled"`
	WatchedMs   int64             `json:"watched_ms"`
}

// MarshalJSON ensures nil slices produce [] instead of null in JSON output.
// Without this, Go's default serialization writes null for nil slices,
// which breaks downstream JSON consumers that expect arrays.
func (r *Result) MarshalJSON() ([]byte, error) {
	type Alias Result
	tmp := Alias(*r)
	if tmp.Conditions == nil {
		tmp.Conditions = []string{}
	}
	if tmp.Failing == nil {
		tmp.Failing = []string{}
	}
	if tmp.Pending == nil {
		tmp.Pending = []string{}
	}
	if tmp.Threads == nil {
		tmp.Threads = []ThreadSummary{}
	}
	return json.Marshal(tmp)
}

// WatchOptions configures the watch behavior.
type WatchOptions struct {
	Interval time.Duration
	Debounce time.Duration
	Timeout  time.Duration
	Mode     Mode
}

// Watch polls until work is detected, with debouncing and signal handling.
func (s *Service) Watch(ctx context.Context, identity *resolver.Identity, opts WatchOptions) (*Result, error) {
	startTime := time.Now()

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// Get initial state
	initialResult, err := s.Fetch(identity, identity.Number)
	if err != nil {
		return nil, fmt.Errorf("fetch initial state: %w", err)
	}

	pr := initialResult.Repository.PullRequest
	initialConditions := Conditions(pr, opts.Mode)
	if len(initialConditions) > 0 {
		return buildResult(pr, initialConditions, false, false, startTime), nil
	}

	// Track last seen state for comparison
	lastConditions := initialConditions

	var (
		debounceTimer *time.Timer
		debounceCh    <-chan time.Time
		ticker        = time.NewTicker(opts.Interval)
	)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			// Check if cancelled by user or timed out
			if errors.Is(ctx.Err(), context.Canceled) {
				return buildResult(pr, lastConditions, false, true, startTime), nil
			}
			return buildResult(pr, lastConditions, true, false, startTime), nil

		case <-ticker.C:
			currentResult, err := s.Fetch(identity, identity.Number)
			if err != nil {
				return nil, fmt.Errorf("fetch: %w", err)
			}

			pr = currentResult.Repository.PullRequest
			currentConditions := Conditions(pr, opts.Mode)

			// Check if conditions changed
			if len(currentConditions) > 0 {
				// Stop existing debounce timer
				if debounceTimer != nil {
					if !debounceTimer.Stop() {
						select {
						case <-debounceTimer.C:
						default:
						}
					}
				}

				// Start new debounce timer
				debounceTimer = time.NewTimer(opts.Debounce)
				debounceCh = debounceTimer.C
				lastConditions = currentConditions
			}

		case <-debounceCh:
			// Debounce fired - conditions have stabilized
			return buildResult(pr, lastConditions, false, false, startTime), nil

		case <-ctx.Done():
			// User cancelled
			return buildResult(pr, lastConditions, false, true, startTime), nil
		}
	}
}

func buildResult(pr *PullRequest, conditions []string, timedOut, cancelled bool, startTime time.Time) *Result {
	failing := FailingChecks(pr)
	if failing == nil {
		failing = []string{}
	}
	pending := PendingChecks(pr)
	if pending == nil {
		pending = []string{}
	}
	annotations := FailingAnnotations(pr)
	if annotations == nil {
		annotations = []CheckAnnotation{}
	}
	threads := ThreadSummaries(pr)
	if threads == nil {
		threads = []ThreadSummary{}
	}
	conditionsNormalized := conditions
	if conditionsNormalized == nil {
		conditionsNormalized = []string{}
	}

	return &Result{
		Conditions:  conditionsNormalized,
		Unresolved:  CountUnresolvedThreads(pr),
		General:     len(pr.Comments.Nodes),
		Conflicts:   HasConflicts(pr),
		Failing:     failing,
		Pending:     pending,
		Annotations: annotations,
		Threads:     threads,
		TimedOut:    timedOut,
		Cancelled:   cancelled,
		WatchedMs:   time.Since(startTime).Milliseconds(),
	}
}
