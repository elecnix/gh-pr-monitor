package monitor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventTypes extracts the ordered Type list from events.
func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// findEvent returns the first event of the given type, or nil.
func findEvent(events []Event, t EventType) *Event {
	for i := range events {
		if events[i].Type == t {
			return &events[i]
		}
	}
	return nil
}

func TestDiff_FirstPollBaselineSilent(t *testing.T) {
	// prev == nil establishes a baseline and must emit nothing, even when the
	// current snapshot is full of pre-existing state.
	curr := &PRStatus{
		State:             "OPEN",
		Conflict:          true,
		FailingChecks:     []string{"CI"},
		PendingChecks:     []string{"Deploy"},
		UnresolvedThreads: []ThreadSummary{{ID: "T1", CommentIDs: []string{"C1"}}},
		GeneralComments:   []GeneralComment{{ID: "G1", Author: "alice", Body: "hi"}},
		ReviewDecision:    "CHANGES_REQUESTED",
		ReviewAuthor:      "bob",
		LastCommit:        CommitSummary{Oid: "abc"},
	}
	assert.Empty(t, Diff(nil, curr))
}

func TestDiff_NewFailingChecks(t *testing.T) {
	prev := &PRStatus{FailingChecks: []string{"CI"}}
	curr := &PRStatus{FailingChecks: []string{"CI", "lint"}}
	events := Diff(prev, curr)
	e := findEvent(events, EventNewFailingChecks)
	require.NotNil(t, e)
	assert.Equal(t, []string{"lint"}, e.Checks)
}

func TestDiff_NoEventWhenSameFailing(t *testing.T) {
	prev := &PRStatus{FailingChecks: []string{"CI"}}
	curr := &PRStatus{FailingChecks: []string{"CI"}}
	assert.Nil(t, findEvent(Diff(prev, curr), EventNewFailingChecks))
}

func TestDiff_CIAllGreen(t *testing.T) {
	t.Run("from failing to clean", func(t *testing.T) {
		prev := &PRStatus{FailingChecks: []string{"CI"}}
		curr := &PRStatus{}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventCIAllGreen))
	})
	t.Run("from pending to clean", func(t *testing.T) {
		prev := &PRStatus{PendingChecks: []string{"Deploy"}}
		curr := &PRStatus{}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventCIAllGreen))
	})
	t.Run("no transition when already clean", func(t *testing.T) {
		prev := &PRStatus{}
		curr := &PRStatus{}
		assert.Nil(t, findEvent(Diff(prev, curr), EventCIAllGreen))
	})
	t.Run("no green when still failing", func(t *testing.T) {
		prev := &PRStatus{FailingChecks: []string{"CI"}}
		curr := &PRStatus{FailingChecks: []string{"CI"}}
		assert.Nil(t, findEvent(Diff(prev, curr), EventCIAllGreen))
	})
}

func TestDiff_NewUnresolvedThreads(t *testing.T) {
	t.Run("brand new thread", func(t *testing.T) {
		prev := &PRStatus{UnresolvedThreads: []ThreadSummary{{ID: "T1", CommentIDs: []string{"C1"}}}}
		curr := &PRStatus{UnresolvedThreads: []ThreadSummary{
			{ID: "T1", CommentIDs: []string{"C1"}},
			{ID: "T2", CommentIDs: []string{"C2"}},
		}}
		e := findEvent(Diff(prev, curr), EventNewUnresolvedThreads)
		require.NotNil(t, e)
		require.Len(t, e.Threads, 1)
		assert.Equal(t, "T2", e.Threads[0].ID)
	})

	t.Run("thread that gained a comment re-fires", func(t *testing.T) {
		prev := &PRStatus{UnresolvedThreads: []ThreadSummary{{ID: "T1", CommentIDs: []string{"C1"}}}}
		curr := &PRStatus{UnresolvedThreads: []ThreadSummary{{ID: "T1", CommentIDs: []string{"C1", "C2"}}}}
		e := findEvent(Diff(prev, curr), EventNewUnresolvedThreads)
		require.NotNil(t, e)
		require.Len(t, e.Threads, 1)
		assert.Equal(t, "T1", e.Threads[0].ID)
	})

	t.Run("unchanged thread does not fire", func(t *testing.T) {
		prev := &PRStatus{UnresolvedThreads: []ThreadSummary{{ID: "T1", CommentIDs: []string{"C1"}}}}
		curr := &PRStatus{UnresolvedThreads: []ThreadSummary{{ID: "T1", CommentIDs: []string{"C1"}}}}
		assert.Nil(t, findEvent(Diff(prev, curr), EventNewUnresolvedThreads))
	})

	t.Run("acked thread disappears without firing", func(t *testing.T) {
		// A thread present in prev is absent in curr (it got acked/resolved).
		// That is not a "new" event — curr has nothing new.
		prev := &PRStatus{UnresolvedThreads: []ThreadSummary{{ID: "T1", CommentIDs: []string{"C1"}}}}
		curr := &PRStatus{}
		assert.Empty(t, Diff(prev, curr))
	})
}

func TestDiff_NewGeneralComments(t *testing.T) {
	t.Run("new comment id fires", func(t *testing.T) {
		prev := &PRStatus{GeneralComments: []GeneralComment{{ID: "G1"}}}
		curr := &PRStatus{GeneralComments: []GeneralComment{{ID: "G1"}, {ID: "G2", Author: "alice", Body: "new"}}}
		e := findEvent(Diff(prev, curr), EventNewGeneralComments)
		require.NotNil(t, e)
		require.Len(t, e.Comments, 1)
		assert.Equal(t, "G2", e.Comments[0].ID)
	})

	t.Run("acked comment disappears without firing", func(t *testing.T) {
		prev := &PRStatus{GeneralComments: []GeneralComment{{ID: "G1"}}}
		curr := &PRStatus{}
		assert.Empty(t, Diff(prev, curr))
	})
}

func TestDiff_Conflict(t *testing.T) {
	t.Run("newly conflicting fires", func(t *testing.T) {
		prev := &PRStatus{Conflict: false}
		curr := &PRStatus{Conflict: true}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventConflict))
	})
	t.Run("still conflicting does not re-fire", func(t *testing.T) {
		prev := &PRStatus{Conflict: true}
		curr := &PRStatus{Conflict: true}
		assert.Nil(t, findEvent(Diff(prev, curr), EventConflict))
	})
}

func TestDiff_ReviewTransitions(t *testing.T) {
	t.Run("approved", func(t *testing.T) {
		prev := &PRStatus{ReviewDecision: ""}
		curr := &PRStatus{ReviewDecision: "APPROVED", ReviewAuthor: "carol"}
		e := findEvent(Diff(prev, curr), EventReviewApproved)
		require.NotNil(t, e)
		assert.Equal(t, "carol", e.ReviewAuthor)
	})
	t.Run("changes requested", func(t *testing.T) {
		prev := &PRStatus{ReviewDecision: "APPROVED"}
		curr := &PRStatus{ReviewDecision: "CHANGES_REQUESTED", ReviewAuthor: "dave"}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventReviewChangesRequested))
	})
	t.Run("dismissed", func(t *testing.T) {
		prev := &PRStatus{ReviewDecision: "APPROVED"}
		curr := &PRStatus{ReviewDecision: "DISMISSED"}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventReviewDismissed))
	})
	t.Run("dismissed via cleared decision", func(t *testing.T) {
		prev := &PRStatus{ReviewDecision: "CHANGES_REQUESTED"}
		curr := &PRStatus{ReviewDecision: ""}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventReviewDismissed))
	})
	t.Run("no change no event", func(t *testing.T) {
		prev := &PRStatus{ReviewDecision: "APPROVED"}
		curr := &PRStatus{ReviewDecision: "APPROVED"}
		assert.Empty(t, Diff(prev, curr))
	})
	t.Run("pending -> empty does not dismiss", func(t *testing.T) {
		prev := &PRStatus{ReviewDecision: ""}
		curr := &PRStatus{ReviewDecision: ""}
		assert.Nil(t, findEvent(Diff(prev, curr), EventReviewDismissed))
	})
}

func TestDiff_NewCommit(t *testing.T) {
	t.Run("oid change fires with parsed metadata", func(t *testing.T) {
		prev := &PRStatus{LastCommit: CommitSummary{Oid: "aaa111"}}
		curr := &PRStatus{LastCommit: CommitSummary{
			Oid:             "bbb222",
			ShortOid:        "bbb222",
			Author:          "grace",
			Coauthors:       []string{"Ada Lovelace"},
			MessageHeadline: "feat: x",
		}}
		e := findEvent(Diff(prev, curr), EventNewCommit)
		require.NotNil(t, e)
		require.NotNil(t, e.Commit)
		assert.Equal(t, "bbb222", e.Commit.Oid)
		assert.Equal(t, "grace", e.Commit.Author)
		assert.Equal(t, []string{"Ada Lovelace"}, e.Commit.Coauthors)
		assert.Equal(t, "feat: x", e.Commit.MessageHeadline)
	})
	t.Run("same oid no event", func(t *testing.T) {
		prev := &PRStatus{LastCommit: CommitSummary{Oid: "aaa111"}}
		curr := &PRStatus{LastCommit: CommitSummary{Oid: "aaa111"}}
		assert.Nil(t, findEvent(Diff(prev, curr), EventNewCommit))
	})
	t.Run("empty curr oid no event", func(t *testing.T) {
		prev := &PRStatus{LastCommit: CommitSummary{Oid: "aaa111"}}
		curr := &PRStatus{LastCommit: CommitSummary{Oid: ""}}
		assert.Nil(t, findEvent(Diff(prev, curr), EventNewCommit))
	})
}

func TestDiff_StateTransitions(t *testing.T) {
	t.Run("merged", func(t *testing.T) {
		prev := &PRStatus{State: "OPEN", Merged: false}
		curr := &PRStatus{State: "MERGED", Merged: true}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventMerged))
		assert.Nil(t, findEvent(Diff(prev, curr), EventClosed))
	})
	t.Run("closed", func(t *testing.T) {
		prev := &PRStatus{State: "OPEN"}
		curr := &PRStatus{State: "CLOSED"}
		assert.NotNil(t, findEvent(Diff(prev, curr), EventClosed))
	})
	t.Run("no transition when already merged", func(t *testing.T) {
		prev := &PRStatus{State: "MERGED", Merged: true}
		curr := &PRStatus{State: "MERGED", Merged: true}
		assert.Empty(t, Diff(prev, curr))
	})
}

func TestDiff_MultipleEventsInOnePass(t *testing.T) {
	prev := &PRStatus{
		State:         "OPEN",
		FailingChecks: []string{"CI"},
	}
	curr := &PRStatus{
		State:           "OPEN",
		Conflict:        true,
		FailingChecks:   []string{"CI", "lint"},
		GeneralComments: []GeneralComment{{ID: "G1", Author: "a", Body: "b"}},
		LastCommit:      CommitSummary{Oid: "new"},
	}
	types := eventTypes(Diff(prev, curr))
	assert.Contains(t, types, EventConflict)
	assert.Contains(t, types, EventNewFailingChecks)
	assert.Contains(t, types, EventNewGeneralComments)
	assert.Contains(t, types, EventNewCommit)
}
