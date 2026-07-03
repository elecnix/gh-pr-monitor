package monitor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elecnix/gh-pr-monitor/internal/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAPI is an injectable ghcli.API for tests.
type fakeAPI struct {
	graphqlFunc func(query string, variables map[string]interface{}, result interface{}) error
}

func (f *fakeAPI) REST(method, path string, params map[string]string, body interface{}, result interface{}) error {
	return errors.New("unexpected REST call")
}

func (f *fakeAPI) GraphQL(query string, variables map[string]interface{}, result interface{}) error {
	if f.graphqlFunc == nil {
		return errors.New("unexpected GraphQL call")
	}
	return f.graphqlFunc(query, variables, result)
}

func assign(result interface{}, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

// thumbsUp returns reaction groups acknowledging a comment.
func thumbsUp() []ReactionGroup {
	return []ReactionGroup{{Content: "THUMBS_UP", Users: struct {
		TotalCount int `json:"totalCount"`
	}{TotalCount: 1}}}
}

func ptr(i int) *int { return &i }

func TestFetch(t *testing.T) {
	t.Run("sends query and unmarshals", func(t *testing.T) {
		api := &fakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
			assert.Contains(t, query, "MonitorPR")
			assert.Equal(t, "octocat", variables["owner"])
			assert.Equal(t, "hello", variables["repo"])
			assert.Equal(t, 7, variables["number"])
			return assign(result, QueryResponse{
				Repository: struct {
					PullRequest *PullRequest `json:"pullRequest"`
				}{PullRequest: &PullRequest{State: "OPEN"}},
			})
		}}
		svc := &Service{API: api}
		got, err := svc.Fetch(&resolver.Identity{Owner: "octocat", Repo: "hello"}, 7)
		require.NoError(t, err)
		assert.Equal(t, "OPEN", got.Repository.PullRequest.State)
	})

	t.Run("nil PR is an error", func(t *testing.T) {
		api := &fakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
			return assign(result, QueryResponse{})
		}}
		svc := &Service{API: api}
		_, err := svc.Fetch(&resolver.Identity{}, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("propagates API error", func(t *testing.T) {
		api := &fakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
			return errors.New("boom")
		}}
		svc := &Service{API: api}
		_, err := svc.Fetch(&resolver.Identity{}, 1)
		require.Error(t, err)
	})
}

func TestSnapshot_Basic(t *testing.T) {
	pr := &PullRequest{
		State:     "OPEN",
		Merged:    false,
		Mergeable: "MERGEABLE",
		Commits: CommitNodes{Nodes: []Commit{{Commit: CommitDetails{
			Oid:             "abcdef1234567890",
			MessageHeadline: "fix things",
		}}}},
	}
	s := Snapshot(pr, SnapshotOptions{})
	assert.Equal(t, "OPEN", s.State)
	assert.False(t, s.Merged)
	assert.False(t, s.Conflict)
	assert.Empty(t, s.UnresolvedThreads)
	assert.Empty(t, s.GeneralComments)
	assert.Equal(t, "abcdef1", s.LastCommit.ShortOid)
	assert.Equal(t, "abcdef1234567890", s.LastCommit.Oid)
	assert.Equal(t, "fix things", s.LastCommit.MessageHeadline)
}

func TestSnapshot_Conflict(t *testing.T) {
	s := Snapshot(&PullRequest{Mergeable: "CONFLICTING"}, SnapshotOptions{})
	assert.True(t, s.Conflict)
}

func TestSnapshot_ThreadFiltering(t *testing.T) {
	pr := &PullRequest{ReviewThreads: ThreadNodes{Nodes: []ReviewThread{
		// unresolved, unacked -> included
		{ID: "T1", IsResolved: false, Path: "a.go", Line: ptr(10), Comments: CommentNodes{Nodes: []Comment{
			{ID: "C1", Body: "please fix"},
		}}},
		// resolved -> excluded
		{ID: "T2", IsResolved: true, Comments: CommentNodes{Nodes: []Comment{{ID: "C2"}}}},
		// unresolved but last comment acked -> excluded
		{ID: "T3", IsResolved: false, Comments: CommentNodes{Nodes: []Comment{
			{ID: "C3a", Body: "nit"},
			{ID: "C3b", Body: "done", ReactionGroups: thumbsUp()},
		}}},
	}}}
	s := Snapshot(pr, SnapshotOptions{})
	require.Len(t, s.UnresolvedThreads, 1)
	assert.Equal(t, "T1", s.UnresolvedThreads[0].ID)
	assert.Equal(t, "a.go", s.UnresolvedThreads[0].Path)
	assert.Equal(t, 10, *s.UnresolvedThreads[0].Line)
	assert.Equal(t, []string{"C1"}, s.UnresolvedThreads[0].CommentIDs)
}

func TestSnapshot_GeneralCommentFiltering(t *testing.T) {
	pr := &PullRequest{Comments: CommentNodes{Nodes: []Comment{
		mkComment("C1", "alice", "hey", nil),
		mkComment("C2", "bob", "acked", thumbsUp()), // acked -> excluded
		mkComment("C3", "dependabot", "bump dep", nil),
	}}}

	t.Run("ack filtering", func(t *testing.T) {
		s := Snapshot(pr, SnapshotOptions{})
		require.Len(t, s.GeneralComments, 2)
		assert.Equal(t, "C1", s.GeneralComments[0].ID)
		assert.Equal(t, "alice", s.GeneralComments[0].Author)
		assert.Equal(t, "C3", s.GeneralComments[1].ID)
	})

	t.Run("ignored bots filtering", func(t *testing.T) {
		s := Snapshot(pr, SnapshotOptions{IgnoredBots: []string{"dependabot"}})
		require.Len(t, s.GeneralComments, 1)
		assert.Equal(t, "C1", s.GeneralComments[0].ID)
	})
}

func mkComment(id, author, body string, reactions []ReactionGroup) Comment {
	c := Comment{ID: id, Body: body, ReactionGroups: reactions}
	c.Author.Login = author
	return c
}

func TestSnapshot_FailingAndPendingChecks(t *testing.T) {
	pr := &PullRequest{Commits: CommitNodes{Nodes: []Commit{{Commit: CommitDetails{
		CheckSuites: SuiteNodes{Nodes: []CheckSuite{
			{Conclusion: "FAILURE", App: AppInfo{Name: "CI"}},
			{Status: "IN_PROGRESS", App: AppInfo{Name: "Deploy"}},
			{CheckRuns: RunNodes{Nodes: []CheckRun{{Name: "unit", Conclusion: "ERROR"}}}},
		}},
		Status: &CommitStatus{Contexts: []StatusContext{
			{State: "FAILURE", Context: "circleci"},
			{State: "PENDING", Context: "buildkite"},
		}},
	}}}}}
	s := Snapshot(pr, SnapshotOptions{})
	assert.ElementsMatch(t, []string{"CI", "unit", "circleci"}, s.FailingChecks)
	assert.ElementsMatch(t, []string{"Deploy", "buildkite"}, s.PendingChecks)
}

func TestSnapshot_ReviewDecision(t *testing.T) {
	t.Run("latest non-pending", func(t *testing.T) {
		pr := &PullRequest{Reviews: ReviewNodes{Nodes: []Review{mkReview("APPROVED", "carol")}}}
		s := Snapshot(pr, SnapshotOptions{})
		assert.Equal(t, "APPROVED", s.ReviewDecision)
		assert.Equal(t, "carol", s.ReviewAuthor)
	})
	t.Run("pending ignored", func(t *testing.T) {
		pr := &PullRequest{Reviews: ReviewNodes{Nodes: []Review{mkReview("PENDING", "dave")}}}
		s := Snapshot(pr, SnapshotOptions{})
		assert.Equal(t, "", s.ReviewDecision)
		assert.Equal(t, "", s.ReviewAuthor)
	})
	t.Run("no reviews", func(t *testing.T) {
		s := Snapshot(&PullRequest{}, SnapshotOptions{})
		assert.Equal(t, "", s.ReviewDecision)
	})
}

func mkReview(state, author string) Review {
	r := Review{State: state}
	r.Author.Login = author
	return r
}

func TestSnapshot_LastCommitAuthorAndCoauthors(t *testing.T) {
	pr := &PullRequest{Commits: CommitNodes{Nodes: []Commit{{Commit: CommitDetails{
		Oid:             "0123456789abcdef",
		MessageHeadline: "feat: add thing",
		Message:         "feat: add thing\n\nBody.\n\nCo-authored-by: Ada Lovelace <ada@example.com>\nCo-authored-by: Alan Turing <alan@example.com>\n",
		Authors: GitActorNodes{Nodes: []GitActor{
			{Name: "Grace Hopper", User: &struct {
				Login string `json:"login"`
			}{Login: "grace"}},
		}},
	}}}}}
	s := Snapshot(pr, SnapshotOptions{})
	assert.Equal(t, "grace", s.LastCommit.Author)
	assert.Equal(t, "0123456", s.LastCommit.ShortOid)
	assert.Equal(t, []string{"Ada Lovelace", "Alan Turing"}, s.LastCommit.Coauthors)
}

func TestSnapshot_AuthorFallbackToName(t *testing.T) {
	pr := &PullRequest{Commits: CommitNodes{Nodes: []Commit{{Commit: CommitDetails{
		Oid:     "aaaa",
		Authors: GitActorNodes{Nodes: []GitActor{{Name: "No Account"}}},
	}}}}}
	s := Snapshot(pr, SnapshotOptions{})
	assert.Equal(t, "No Account", s.LastCommit.Author)
	assert.Equal(t, "aaaa", s.LastCommit.ShortOid) // shorter than 7, unchanged
}

func TestParseCoauthors(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    []string
	}{
		{"empty", "", nil},
		{"none", "just a message", nil},
		{"single", "msg\n\nCo-authored-by: Ada <ada@x.com>", []string{"Ada"}},
		{"case insensitive", "co-AUTHORED-by: Bob <b@x.com>", []string{"Bob"}},
		{"strips email keeps name", "Co-authored-by: Jane Doe <jane@example.com>", []string{"Jane Doe"}},
		{"no email", "Co-authored-by: Nameonly", []string{"Nameonly"}},
		{"dedup", "Co-authored-by: Ada <a@x>\nCo-authored-by: Ada <a@x>", []string{"Ada"}},
		{"order", "Co-authored-by: B <b@x>\nCo-authored-by: A <a@x>", []string{"B", "A"}},
		{"leading whitespace", "   Co-authored-by:   Spaced   <s@x>  ", []string{"Spaced"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseCoauthors(tt.message))
		})
	}
}

func TestIsAcknowledged(t *testing.T) {
	assert.True(t, isAcknowledged(&Comment{ReactionGroups: thumbsUp()}))
	assert.False(t, isAcknowledged(&Comment{}))
	// group present but zero users -> not acknowledged
	assert.False(t, isAcknowledged(&Comment{ReactionGroups: []ReactionGroup{{Content: "THUMBS_UP"}}}))
	// other reaction -> not acknowledged
	assert.False(t, isAcknowledged(&Comment{ReactionGroups: []ReactionGroup{{Content: "HEART", Users: struct {
		TotalCount int `json:"totalCount"`
	}{TotalCount: 3}}}}))
}

func TestSnapshot_JSONRoundTrip(t *testing.T) {
	s := Snapshot(&PullRequest{State: "OPEN", Mergeable: "MERGEABLE"}, SnapshotOptions{})
	data, err := json.Marshal(s)
	require.NoError(t, err)
	// nil-safe empty arrays
	assert.True(t, strings.Contains(string(data), `"unresolved_threads":[]`))
	assert.True(t, strings.Contains(string(data), `"general_comments":[]`))
}
