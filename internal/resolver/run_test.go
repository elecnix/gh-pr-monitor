package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRun(t *testing.T) {
	t.Run("explicit repo and host", func(t *testing.T) {
		id, err := ResolveRun(30433642, "octo/demo", "github.com")
		require.NoError(t, err)
		assert.Equal(t, Identity{
			Owner:  "octo",
			Repo:   "demo",
			Host:   "github.com",
			RunID:  30433642,
			Target: "run",
		}, id)
	})

	t.Run("sanitizes host and defaults when empty", func(t *testing.T) {
		id, err := ResolveRun(1, "octo/demo", "")
		require.NoError(t, err)
		assert.Equal(t, "github.com", id.Host)
	})

	t.Run("rejects non-positive run id", func(t *testing.T) {
		_, err := ResolveRun(0, "octo/demo", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run id")
	})

	t.Run("rejects missing repo", func(t *testing.T) {
		_, err := ResolveRun(1, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repository")
	})

	t.Run("rejects malformed repo", func(t *testing.T) {
		_, err := ResolveRun(1, "not-a-slash", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "owner/repo")
	})
}
