package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/elecnix/gh-pr-monitor/internal/prefs"
	"github.com/elecnix/gh-pr-monitor/internal/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkCommit(oid string, failing []string) Commit {
	runs := make([]CheckRun, 0, len(failing))
	for _, name := range failing {
		runs = append(runs, CheckRun{Name: name, Conclusion: "FAILURE"})
	}
	return Commit{Commit: CommitDetails{
		Oid:             oid,
		MessageHeadline: "headline",
		CheckSuites:     SuiteNodes{Nodes: []CheckSuite{{App: AppInfo{Name: "CI"}, CheckRuns: RunNodes{Nodes: runs}}}},
	}}
}

func mkPR(state string, merged bool, oid string, failing []string) *PullRequest {
	return &PullRequest{
		State:   state,
		Merged:  merged,
		Commits: CommitNodes{Nodes: []Commit{mkCommit(oid, failing)}},
	}
}

// scriptedAPI returns each response in order, repeating the last one.
func scriptedAPI(responses []*PullRequest) *fakeAPI {
	call := 0
	return &fakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
		idx := call
		if idx >= len(responses) {
			idx = len(responses) - 1
		}
		call++
		return assign(result, QueryResponse{Repository: struct {
			PullRequest *PullRequest `json:"pullRequest"`
		}{PullRequest: responses[idx]}})
	}}
}

func testRunOptions() RunOptions {
	return RunOptions{
		Identity: resolver.Identity{Owner: "o", Repo: "r", Number: 7, Host: "github.com"},
		Prefs:    prefs.DefaultPreferences(),
		Interval: 60 * time.Second,
		Now:      func() time.Time { return time.Unix(0, 0).UTC() },
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}
}

func typesOf(ns []Notification) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Type
	}
	return out
}

func TestRun_StreamsEventsUntilMerged(t *testing.T) {
	svc := &Service{API: scriptedAPI([]*PullRequest{
		mkPR("OPEN", false, "aaaaaaa", nil),                // baseline, clean
		mkPR("OPEN", false, "aaaaaaa", []string{"build"}),  // failing appears
		mkPR("MERGED", true, "aaaaaaa", []string{"build"}), // merged (keep failing -> no all-green)
	})}

	var got []Notification
	err := Run(context.Background(), svc, testRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	types := typesOf(got)
	require.NotEmpty(t, types)
	assert.Equal(t, firstPollType, types[0])
	assert.Contains(t, types, string(EventNewFailingChecks))
	assert.Equal(t, string(EventMerged), types[len(types)-1])

	var failing *Notification
	for i := range got {
		if got[i].Type == string(EventNewFailingChecks) {
			failing = &got[i]
		}
	}
	require.NotNil(t, failing)
	assert.Equal(t, "❌ Failing CI checks on o/r#7: build", failing.Message)
	assert.Equal(t, []string{"build"}, failing.FailingChecks)
}

func TestRun_NoChangeEmitsNothing(t *testing.T) {
	svc := &Service{API: scriptedAPI([]*PullRequest{
		mkPR("OPEN", false, "aaaaaaa", nil),
		mkPR("OPEN", false, "aaaaaaa", nil), // identical -> no events
		mkPR("MERGED", true, "aaaaaaa", nil),
	})}

	var got []Notification
	err := Run(context.Background(), svc, testRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	assert.Equal(t, []string{firstPollType, string(EventMerged)}, typesOf(got))
}

func TestRun_ContextCancelStops(t *testing.T) {
	svc := &Service{API: scriptedAPI([]*PullRequest{mkPR("OPEN", false, "aaaaaaa", nil)})}
	opts := testRunOptions()
	opts.Sleep = func(context.Context, time.Duration) error { return context.Canceled }

	var got []Notification
	err := Run(context.Background(), svc, opts, func(n Notification) { got = append(got, n) })
	assert.ErrorIs(t, err, context.Canceled)
	require.NotEmpty(t, got)
	assert.Equal(t, firstPollType, got[0].Type) // first-poll emitted before the (cancelling) sleep
}

func TestRun_AlreadyMergedAtStartup(t *testing.T) {
	svc := &Service{API: scriptedAPI([]*PullRequest{mkPR("MERGED", true, "aaaaaaa", nil)})}

	var got []Notification
	err := Run(context.Background(), svc, testRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)
	// first-poll baseline, then a synthesized terminal notification so the
	// consumer learns why the stream ends.
	assert.Equal(t, []string{firstPollType, string(EventMerged)}, typesOf(got))
}

func TestOnce_EmitsCurrentActionable(t *testing.T) {
	pr := &PullRequest{
		State:    "OPEN",
		Comments: CommentNodes{Nodes: []Comment{mkComment("c1", "alice", "please fix", nil)}},
		ReviewThreads: ThreadNodes{Nodes: []ReviewThread{{
			ID:       "t1",
			Comments: CommentNodes{Nodes: []Comment{mkComment("tc1", "bob", "nit", nil)}},
		}}},
		Commits: CommitNodes{Nodes: []Commit{mkCommit("abc1234def", []string{"build"})}},
	}
	svc := &Service{API: scriptedAPI([]*PullRequest{pr})}

	var got []Notification
	err := Once(context.Background(), svc, testRunOptions(), func(n Notification) { got = append(got, n) })
	require.NoError(t, err)

	types := typesOf(got)
	assert.Equal(t, firstPollType, types[0])
	assert.Contains(t, types, string(EventNewFailingChecks))
	assert.Contains(t, types, string(EventNewUnresolvedThreads))
	assert.Contains(t, types, string(EventNewGeneralComments))
	assert.Contains(t, types, string(EventNewCommit))
}

func TestIdleInterval(t *testing.T) {
	base := 60 * time.Second
	assert.Equal(t, base, idleInterval(base, 0))
	assert.Equal(t, base, idleInterval(base, 3))              // growth starts after 3
	assert.Equal(t, 2*base, idleInterval(base, 4))            // base * 2^1
	assert.Equal(t, maxIdleInterval, idleInterval(base, 100)) // capped
}
