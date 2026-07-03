# Using `gh-pr-monitor` with Claude Code

`gh pr-monitor monitor` is built to be driven by [Claude Code](https://claude.com/claude-code)'s **persistent `Monitor`** tool. The command runs a poll loop and prints **one NDJSON line per genuinely-new change** on a pull request; Claude Code's `Monitor` streams each line back into the session as a notification, so the agent reacts to review comments, CI failures, conflicts, and new commits as they happen — without polling and without burning tokens between events.

This mirrors the behavior of the `pi-ghpr-monitor` extension for the `pi` agent, but instead of the tool injecting notifications itself, the CLI does the polling + change-detection and the Claude Code harness does the delivery.

## The recipe

Ask Claude Code to monitor a PR, and it registers a persistent monitor whose command is this tool:

```
Monitor({
  command: "gh pr-monitor monitor -R owner/repo 42",
  persistent: true,
  description: "PR owner/repo#42 events",
})
```

- `persistent: true` keeps the watch alive for the whole session (no timeout) — right for PR monitoring.
- Each stdout line (one NDJSON event) becomes a session notification, delivered even while Claude is idle. If a line arrives mid-turn, the harness queues it and flushes when the turn ends — the same "don't interrupt an active turn" behavior `pi-ghpr-monitor` implemented by hand.
- The command **auto-stops** (exits 0) when the PR is merged or closed, ending the watch. To stop earlier, `TaskStop` the monitor.

You generally do not type the `Monitor(...)` call yourself — you say something like _"monitor PR 42 in this repo and address review comments as they come in,"_ and Claude sets up the watch.

## Why the loop is safe to leave running

- **No token cost between events.** The poll and change-detection run inside the CLI process; Claude's context is only touched when a real event is emitted.
- **No duplicate notifications.** The loop diffs each poll against the previous snapshot and only emits genuinely-new changes. 👍-acknowledged comments and threads are dropped, so re-reacting to the same bot comment doesn't re-fire.
- **Adaptive backoff.** After 3 no-change polls the interval grows exponentially (capped at 5 minutes) and resets on any change, so an idle PR is cheap to watch.
- **Resilient.** Transient GitHub API errors are logged to stderr and retried with a separate backoff; the loop does not crash.

## Reacting to events

Each NDJSON line has a stable `type` and a rendered `message`, plus event-relevant fields. See [SCHEMAS.md](SCHEMAS.md) for the full field list. Typical agent reactions:

| `type`                     | Suggested reaction                                                                       |
| -------------------------- | ---------------------------------------------------------------------------------------- |
| `new-unresolved-threads`   | Reply to and resolve each thread (`gh pr-monitor comments reply` / `threads resolve`)    |
| `new-general-comments`     | Address the comment; 👍-react to acknowledge non-actionable ones (`gh pr-monitor react`) |
| `new-failing-checks`       | Inspect the failing checks and push a fix                                                |
| `conflict`                 | Rebase / resolve the merge conflict                                                      |
| `review-changes-requested` | Address the requested changes                                                            |
| `new-commit`               | Re-check the PR description still reflects the changes                                   |
| `merged` / `closed`        | Monitoring has stopped — wrap up                                                         |

Because 👍 acknowledgment silences a comment, the loop-breaker is: **fix or reply, then react 👍 (or resolve the thread)**, and that item won't notify again.

## Customizing the notification wording

Notification text is templated and user-overridable. Edit:

```
${XDG_CONFIG_HOME:-~/.config}/gh-pr-monitor/preferences.json
```

Each template is a string with `{token}` placeholders (e.g. `{prLabel}`, `{failingChecks}`, `{commitAuthor}`). A template value with no `{token}` is rejected; set a key to `null` to reset it to the built-in default. Non-template config keys `ignoredBots` (author logins to silence) and `retriggerComments` live in the same file. See the `monitor` section of the [README](../README.md) for the token and key list.

## Relationship to `await`

`await` returns **once** when a PR needs attention (good for a single "wait until X" gate). `monitor` runs **continuously**, emitting an event per change until merge/close — that is the shape a persistent watcher wants. Use `await` for one-shot gating; use `monitor` under Claude Code's `Monitor`.
