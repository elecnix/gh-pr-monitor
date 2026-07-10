package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFailedRunLogs builds a FailedRunLogsFn that returns the given output (and
// nil error), recording the args it was called with.
func fakeFailedRunLogs(out string) (func(owner, repo string, runID int) (string, error), *callLog) {
	c := &callLog{}
	return func(owner, repo string, runID int) (string, error) {
		c.owner = owner
		c.repo = repo
		c.runID = runID
		c.count++
		return out, nil
	}, c
}

type callLog struct {
	owner string
	repo  string
	runID int
	count int
}

func TestRunRun_FailureIncludesFailedLogSnippet(t *testing.T) {
	logs := "build\tRun build job\n##[error]compile failed\nexit code 1"
	fn, calls := fakeFailedRunLogs(logs)
	svc := &Service{
		API: scriptedRunAPI([]*WorkflowRun{
			mkWorkflowRun("in_progress", ""),
			mkWorkflowRun("completed", "failure"),
		}),
		FailedRunLogsFn: fn,
	}

	var got []Notification
	err := Run(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	var completed *Notification
	for i := range got {
		if got[i].Type == string(EventRunCompleted) {
			completed = &got[i]
		}
	}
	require.NotNil(t, completed)
	assert.Equal(t, "failure", completed.Conclusion)
	// The fetcher was called with the run identity.
	assert.Equal(t, 1, calls.count)
	assert.Equal(t, "octo", calls.owner)
	assert.Equal(t, "demo", calls.repo)
	assert.Equal(t, 30433642, calls.runID)
	// The detail embeds the failing job name and the error snippet.
	assert.Contains(t, completed.Detail, "build")
	assert.Contains(t, completed.Detail, "##[error]compile failed")
}

func TestRunRun_AlreadyFailedAtStartupIncludesLogSnippet(t *testing.T) {
	logs := "##[error]deploy step failed"
	fn, calls := fakeFailedRunLogs(logs)
	svc := &Service{
		API:             scriptedRunAPI([]*WorkflowRun{mkWorkflowRun("completed", "failure")}),
		FailedRunLogsFn: fn,
	}

	var got []Notification
	err := Run(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	var completed *Notification
	for i := range got {
		if got[i].Type == string(EventRunCompleted) {
			completed = &got[i]
		}
	}
	require.NotNil(t, completed)
	assert.Equal(t, 1, calls.count)
	assert.Contains(t, completed.Detail, "##[error]deploy step failed")
}

func TestRunRun_FailureLogTruncatedToFiftyLines(t *testing.T) {
	// 120 non-empty lines.
	long := strings.TrimRight(strings.Repeat("line\n", 120), "\n")
	fn, _ := fakeFailedRunLogs(long)
	svc := &Service{
		API:             scriptedRunAPI([]*WorkflowRun{mkWorkflowRun("completed", "failure")}),
		FailedRunLogsFn: fn,
	}

	var got []Notification
	err := Run(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	var completed *Notification
	for i := range got {
		if got[i].Type == string(EventRunCompleted) {
			completed = &got[i]
		}
	}
	require.NotNil(t, completed)
	require.NotEmpty(t, completed.Detail)
	lines := strings.Split(completed.Detail, "\n")
	// 50 kept lines + 1 truncation marker.
	assert.Equal(t, maxFailedLogLines+1, len(lines))
	assert.Contains(t, completed.Detail, "truncated")
	assert.Contains(t, completed.Detail, "70 more")
}

func TestRunRun_FailureLogFetchErrorOmitsDetail(t *testing.T) {
	svc := &Service{
		API: scriptedRunAPI([]*WorkflowRun{mkWorkflowRun("completed", "failure")}),
		FailedRunLogsFn: func(string, string, int) (string, error) {
			return "", errors.New("boom")
		},
	}

	var got []Notification
	err := Run(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	var completed *Notification
	for i := range got {
		if got[i].Type == string(EventRunCompleted) {
			completed = &got[i]
		}
	}
	require.NotNil(t, completed)
	assert.Empty(t, completed.Detail)
}

func TestRunRun_SuccessDoesNotFetchLogs(t *testing.T) {
	fn, calls := fakeFailedRunLogs("should not be called")
	svc := &Service{
		API: scriptedRunAPI([]*WorkflowRun{
			mkWorkflowRun("in_progress", ""),
			mkWorkflowRun("completed", "success"),
		}),
		FailedRunLogsFn: fn,
	}

	var got []Notification
	err := Run(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)
	assert.Equal(t, 0, calls.count, "logs must not be fetched for a successful run")

	var completed *Notification
	for i := range got {
		if got[i].Type == string(EventRunCompleted) {
			completed = &got[i]
		}
	}
	require.NotNil(t, completed)
	assert.Empty(t, completed.Detail)
}

func TestRunRun_TimedOutConclusionFetchesLogs(t *testing.T) {
	fn, calls := fakeFailedRunLogs("##[error]timed out")
	svc := &Service{
		API: scriptedRunAPI([]*WorkflowRun{
			mkWorkflowRun("queued", ""),
			mkWorkflowRun("in_progress", ""),
			mkWorkflowRun("completed", "timed_out"),
		}),
		FailedRunLogsFn: fn,
	}

	var got []Notification
	err := Run(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)
	assert.Equal(t, 1, calls.count, "logs must be fetched for a timed-out run")
}

func TestRunRun_NilFetcherOmitsDetail(t *testing.T) {
	// No FailedRunLogsFn wired (e.g. older callers): failed run still emits,
	// just without a detail body and without panicking.
	svc := &Service{API: scriptedRunAPI([]*WorkflowRun{mkWorkflowRun("completed", "failure")})}

	var got []Notification
	err := Run(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	var completed *Notification
	for i := range got {
		if got[i].Type == string(EventRunCompleted) {
			completed = &got[i]
		}
	}
	require.NotNil(t, completed)
	assert.Empty(t, completed.Detail)
}

func TestOnceRun_FailureIncludesFailedLogSnippet(t *testing.T) {
	fn, calls := fakeFailedRunLogs("##[error]once failed")
	svc := &Service{
		API:             scriptedRunAPI([]*WorkflowRun{mkWorkflowRun("completed", "failure")}),
		FailedRunLogsFn: fn,
	}

	var got []Notification
	err := Once(context.Background(), svc, runRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	var completed *Notification
	for i := range got {
		if got[i].Type == string(EventRunCompleted) {
			completed = &got[i]
		}
	}
	require.NotNil(t, completed)
	assert.Equal(t, 1, calls.count)
	assert.Contains(t, completed.Detail, "##[error]once failed")
}

func TestTruncateLog(t *testing.T) {
	t.Run("empty stays empty", func(t *testing.T) {
		assert.Equal(t, "", truncateLog("", 50))
	})
	t.Run("under limit unchanged", func(t *testing.T) {
		in := strings.TrimRight("a\nb\nc", "\n")
		assert.Equal(t, in, truncateLog("a\nb\nc\n", 50))
	})
	t.Run("over limit truncates with marker", func(t *testing.T) {
		in := strings.TrimRight(strings.Repeat("x\n", 60), "\n") // 60 lines
		out := truncateLog(in, 50)
		lines := strings.Split(out, "\n")
		require.Len(t, lines, 51) // 50 + marker
		assert.Contains(t, lines[50], "10 more")
		assert.Contains(t, lines[50], "truncated")
	})
	t.Run("exactly at limit no marker", func(t *testing.T) {
		in := strings.TrimRight(strings.Repeat("x\n", 50), "\n") // 50 lines
		out := truncateLog(in, 50)
		assert.Equal(t, in, out)
	})
}

func TestIsRunFailureConclusion(t *testing.T) {
	for _, c := range []string{"failure", "timed_out", "cancelled", "action_required"} {
		assert.True(t, isRunFailureConclusion(c), c)
	}
	for _, c := range []string{"success", "neutral", "skipped", "stale", ""} {
		assert.False(t, isRunFailureConclusion(c), c)
	}
}
