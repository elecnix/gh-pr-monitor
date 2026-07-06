package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elecnix/gh-pr-monitor/internal/ghcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openPRWithFailingCheck is a monitor GraphQL payload for an open PR with one
// failing check run.
func openPRWithFailingCheck() obj {
	return obj{
		"repository": obj{
			"pullRequest": obj{
				"state":     "OPEN",
				"merged":    false,
				"mergeable": "MERGEABLE",
				"commits": obj{"nodes": []interface{}{obj{"commit": obj{
					"oid": "abcdef1234",
					"checkSuites": obj{"nodes": []interface{}{obj{
						"app":       obj{"name": "CI"},
						"checkRuns": obj{"nodes": []interface{}{obj{"name": "build", "conclusion": "FAILURE"}}},
					}}},
				}}}},
			},
		},
	}
}

func TestMonitorOnceEmitsNDJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GH_HOST", "")
	originalFactory := apiClientFactory
	defer func() { apiClientFactory = originalFactory }()

	fake := &commandFakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
		require.Contains(t, query, "MonitorPR")
		return assignJSON(result, openPRWithFailingCheck())
	}}
	apiClientFactory = func(string) ghcli.API { return fake }

	root := newRootCommand()
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"monitor", "7", "-R", "o/r", "--once"})
	require.NoError(t, root.Execute())

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	// Every line is valid JSON.
	var firstPollSeen, failingSeen bool
	for _, ln := range lines {
		var n map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(ln), &n), "line not valid json: %s", ln)
		switch n["type"] {
		case "first-poll":
			firstPollSeen = true
		case "new-failing-checks":
			failingSeen = true
			assert.Equal(t, "o/r#7", n["pr_label"])
		}
	}
	assert.True(t, firstPollSeen, "expected a first-poll event")
	assert.True(t, failingSeen, "expected a new-failing-checks event")
}

func TestMonitorOnceTextMode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GH_HOST", "")
	originalFactory := apiClientFactory
	defer func() { apiClientFactory = originalFactory }()

	fake := &commandFakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
		return assignJSON(result, openPRWithFailingCheck())
	}}
	apiClientFactory = func(string) ghcli.API { return fake }

	root := newRootCommand()
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"monitor", "7", "-R", "o/r", "--once", "--text"})
	require.NoError(t, root.Execute())

	out := stdout.String()
	assert.NotContains(t, out, `"type":`) // not JSON
	// The PR label is OSC-8 linkified in --text mode; the surrounding rendered
	// text is unchanged.
	assert.Contains(t, out, "\x1b]8;;https://github.com/o/r/pull/7\x1b\\o/r#7\x1b]8;;\x1b\\")
	assert.Contains(t, out, "📡 Monitoring ")
	assert.Contains(t, out, "❌ Failing CI checks on ")
	assert.Contains(t, out, ": build")
}

func TestMonitorRequiresPR(t *testing.T) {
	root := newRootCommand()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"monitor", "-R", "o/r"})
	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pull request number or URL is required")
}

func TestMonitorOnceEmitsNDJSON_DefaultCommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GH_HOST", "")
	originalFactory := apiClientFactory
	defer func() { apiClientFactory = originalFactory }()

	fake := &commandFakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
		require.Contains(t, query, "MonitorPR")
		return assignJSON(result, openPRWithFailingCheck())
	}}
	apiClientFactory = func(string) ghcli.API { return fake }

	root := newRootCommand()
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"7", "-R", "o/r", "--once"})
	require.NoError(t, root.Execute())

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	var firstPollSeen, failingSeen bool
	for _, ln := range lines {
		var n map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(ln), &n), "line not valid json: %s", ln)
		switch n["type"] {
		case "first-poll":
			firstPollSeen = true
		case "new-failing-checks":
			failingSeen = true
			assert.Equal(t, "o/r#7", n["pr_label"])
		}
	}
	assert.True(t, firstPollSeen, "expected a first-poll event")
	assert.True(t, failingSeen, "expected a new-failing-checks event")
}

func TestMonitorOnceEmitsNDJSON_WatchAlias(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GH_HOST", "")
	originalFactory := apiClientFactory
	defer func() { apiClientFactory = originalFactory }()

	fake := &commandFakeAPI{graphqlFunc: func(query string, variables map[string]interface{}, result interface{}) error {
		require.Contains(t, query, "MonitorPR")
		return assignJSON(result, openPRWithFailingCheck())
	}}
	apiClientFactory = func(string) ghcli.API { return fake }

	root := newRootCommand()
	stdout := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"watch", "7", "-R", "o/r", "--once"})
	require.NoError(t, root.Execute())

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	var firstPollSeen, failingSeen bool
	for _, ln := range lines {
		var n map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(ln), &n), "line not valid json: %s", ln)
		switch n["type"] {
		case "first-poll":
			firstPollSeen = true
		case "new-failing-checks":
			failingSeen = true
			assert.Equal(t, "o/r#7", n["pr_label"])
		}
	}
	assert.True(t, firstPollSeen, "expected a first-poll event")
	assert.True(t, failingSeen, "expected a new-failing-checks event")
}

func TestRootRejectsTooManyArgs(t *testing.T) {
	root := newRootCommand()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"7", "8"})
	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts at most")
}
