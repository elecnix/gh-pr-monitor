package monitor

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkWorkflowRun(status, conclusion string) *WorkflowRun {
	return &WorkflowRun{
		ID:           30433642,
		Name:         "deploy",
		DisplayTitle: "Deploy to prod",
		Event:        "workflow_dispatch",
		Status:       status,
		Conclusion:   conclusion,
		HeadBranch:   "main",
		HeadSHA:      "abcdef1234567890",
		HTMLURL:      "https://github.com/octo/demo/actions/runs/30433642",
		RunNumber:    42,
	}
}

func TestFetchRun(t *testing.T) {
	t.Run("sends GET and unmarshals", func(t *testing.T) {
		api := &fakeAPI{restFunc: func(method, path string, params map[string]string, body interface{}, result interface{}) error {
			assert.Equal(t, "GET", method)
			assert.Equal(t, "repos/octo/demo/actions/runs/30433642", path)
			return assign(result, mkWorkflowRun("in_progress", ""))
		}}
		svc := &Service{API: api}
		got, err := svc.FetchRun("octo", "demo", 30433642)
		require.NoError(t, err)
		assert.Equal(t, "deploy", got.Name)
		assert.Equal(t, "in_progress", got.Status)
		assert.Equal(t, 30433642, got.ID)
	})

	t.Run("propagates API error", func(t *testing.T) {
		api := &fakeAPI{restFunc: func(string, string, map[string]string, interface{}, interface{}) error {
			return errors.New("boom")
		}}
		svc := &Service{API: api}
		_, err := svc.FetchRun("o", "r", 1)
		require.Error(t, err)
	})
}

func TestSnapshotRun(t *testing.T) {
	t.Run("distills fields and short sha", func(t *testing.T) {
		s := SnapshotRun(mkWorkflowRun("completed", "success"))
		assert.Equal(t, 30433642, s.RunID)
		assert.Equal(t, "completed", s.Status)
		assert.Equal(t, "success", s.Conclusion)
		assert.Equal(t, "deploy", s.Name)
		assert.Equal(t, "workflow_dispatch", s.Event)
		assert.Equal(t, "main", s.HeadBranch)
		assert.Equal(t, "abcdef1234567890", s.HeadSHA)
		assert.Equal(t, "abcdef1", s.ShortSHA)
		assert.Equal(t, 42, s.RunNumber)
		assert.True(t, s.IsTerminal())
	})

	t.Run("in_progress is not terminal", func(t *testing.T) {
		s := SnapshotRun(mkWorkflowRun("in_progress", ""))
		assert.False(t, s.IsTerminal())
	})
}

func TestDiffRun(t *testing.T) {
	t.Run("nil prev is silent", func(t *testing.T) {
		curr := SnapshotRun(mkWorkflowRun("completed", "success"))
		assert.Empty(t, DiffRun(nil, curr))
	})

	t.Run("queued to in_progress emits run-in-progress", func(t *testing.T) {
		prev := SnapshotRun(mkWorkflowRun("queued", ""))
		curr := SnapshotRun(mkWorkflowRun("in_progress", ""))
		events := DiffRun(prev, curr)
		require.Len(t, events, 1)
		assert.Equal(t, EventRunInProgress, events[0].Type)
	})

	t.Run("in_progress to completed emits run-completed with conclusion", func(t *testing.T) {
		prev := SnapshotRun(mkWorkflowRun("in_progress", ""))
		curr := SnapshotRun(mkWorkflowRun("completed", "failure"))
		events := DiffRun(prev, curr)
		require.Len(t, events, 1)
		assert.Equal(t, EventRunCompleted, events[0].Type)
		assert.Equal(t, "failure", events[0].RunConclusion)
	})

	t.Run("completed to completed no new event", func(t *testing.T) {
		prev := SnapshotRun(mkWorkflowRun("completed", "success"))
		curr := SnapshotRun(mkWorkflowRun("completed", "success"))
		assert.Empty(t, DiffRun(prev, curr))
	})
}
