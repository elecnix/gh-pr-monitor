---
name: gh-pr-monitor
description: View, reply, and resolve inline GitHub PR review comments from the terminal. Monitor PRs for new comments, CI failures, conflicts, and other events — built for coding agents.
---

# gh-pr-monitor

A GitHub CLI extension built for coding agents (Claude Code, pi, etc.) — inline PR review comments, thread management, live monitoring with streaming NDJSON events, and structured JSON output.

## When to Use

Use when you need to interact with a PR: view/reply/resolve review threads, create reviews with inline comments, manage draft status, add reactions, poll for attention (`await`), or continuously monitor for events (`monitor`).

## Installation

```sh
gh extension install elecnix/gh-pr-monitor
```

## Core Commands

All commands accept `-R owner/repo`. IDs use GraphQL format (`PRR_*`, `PRRT_*`, `PRRC_*`). Use `--body-file -` for stdin input.

### View Reviews & Threads

```sh
gh pr-monitor review view -R owner/repo --pr <number>
```

Key filters: `--unresolved`, `--not_outdated`, `--tail 1`, `--reviewer <login>`, `--include-comment-node-id`.

### Reply to Threads

```sh
gh pr-monitor comments reply <pr-number> -R owner/repo --thread-id <id> --body "..."
```

### List / View / Resolve Threads

```sh
gh pr-monitor threads list -R owner/repo <pr> --unresolved --mine
gh pr-monitor threads view <thread_id> [<thread_id> ...]
gh pr-monitor threads resolve -R owner/repo <pr> --thread-id <id>
gh pr-monitor threads unresolve -R owner/repo <pr> --thread-id <id>
```

### Reactions

```sh
gh pr-monitor react <comment_id> --type thumbs_up
```

Valid types: `thumbs_up`, `thumbs_down`, `laugh`, `hooray`, `confused`, `heart`, `rocket`, `eyes`.

### Draft Status

```sh
gh pr-monitor draft status -R owner/repo <pr>
gh pr-monitor draft mark -R owner/repo <pr>
gh pr-monitor draft ready -R owner/repo <pr>
gh pr-monitor draft list -R owner/repo
```

### Create & Submit Reviews

Start a pending review, add inline comments, then submit:

```sh
gh pr-monitor review --start -R owner/repo <pr>
gh pr-monitor review --add-comment -R owner/repo <pr> --review-id <id> --path <file> --line <n> --body "..."
gh pr-monitor review --submit -R owner/repo <pr> --review-id <id> --event <APPROVE|REQUEST_CHANGES|COMMENT> --body "..."
```

Edit or delete pending comments: `--edit-comment` / `--delete-comment` with `--comment-id`. Pin start to a commit with `--commit <sha>`.

### Monitor a PR

Continuously stream one NDJSON event per genuinely-new change until merge/close:

```sh
gh pr-monitor monitor -R owner/repo <pr>        # NDJSON stream
gh pr-monitor monitor --text -R owner/repo <pr> # Human-readable
gh pr-monitor monitor --once -R owner/repo <pr> # One-shot, then exit
```

Flags: `--interval` (default 60s, min 10), `--timeout` (default 0 = forever), `--ignored-bots <a,b>`.

**Event types:**

| Type | Description |
|------|-------------|
| `new-unresolved-threads` | New review threads |
| `new-general-comments` | New general PR comments |
| `new-failing-checks` | Newly failing CI |
| `ci-all-green` | All CI passing |
| `conflict` | Merge conflicts |
| `review-approved` | PR approved |
| `review-changes-requested` | Changes requested |
| `review-dismissed` | Review dismissed |
| `new-commit` | New commit pushed |
| `merged` | PR merged |
| `closed` | PR closed |

**Claude Code integration:** Wrap in a persistent `Monitor` tool — each NDJSON line becomes a session notification:

```
Monitor({ command: "gh pr-monitor monitor -R owner/repo 42", persistent: true })
```

**Adaptive backoff:** After 3 no-change polls, interval grows exponentially (cap 5min), resets on any change. Transient errors retry with doubling backoff — the loop doesn't crash.

### Await (poll until attention needed)

```sh
gh pr-monitor await --check-only -R owner/repo <pr>   # One-shot
gh pr-monitor await -R owner/repo <pr>                # Poll until work (default: 1d timeout, 5min interval)
gh pr-monitor await --mode comments --timeout 3600 -R owner/repo <pr>
```

Exit codes: `0` = work detected, `1` = error, `2` = timed out / no work.  
Flags: `--mode <all|comments|conflicts|actions>`, `--timeout`, `--interval`, `--debounce`, `--check-only`.

## Critical Workflows

### Claim a Thread Before Working on It

Add an 👀 reaction to the **first** comment in a thread to signal you're working on it — prevents duplicate effort from other agents:

```sh
gh pr-monitor react <comment_node_id> --type eyes
```

A reaction does NOT resolve a thread. After addressing, reply then resolve with `threads resolve`.

### Reply to All Unresolved Comments

1. List: `gh pr-monitor threads list --unresolved -R owner/repo <pr>`
2. Reply: `gh pr-monitor comments reply <pr> -R owner/repo --thread-id <id> --body "..."`
3. Resolve: `gh pr-monitor threads resolve <pr> -R owner/repo --thread-id <id>`

If non-actionable, resolve with a brief reply explaining why.

### Create Review with Inline Comments

1. `gh pr-monitor review --start -R owner/repo <pr>`
2. `gh pr-monitor review --add-comment ...`
3. `gh pr-monitor review --submit -R owner/repo <pr> --review-id <id> --event REQUEST_CHANGES --body "Summary"`

### Get Actionable Unresolved Comments

```sh
gh pr-monitor review view --unresolved --not_outdated --tail 1 -R owner/repo --pr $(gh pr view --json number -q .number)
```

## Key Notes

- Comments can only be edited/deleted while the review is pending (before submit).
- Numeric review IDs are rejected — always use GraphQL node IDs (`PRR_*`).
- Monitor notification templates are overridable via `${XDG_CONFIG_HOME:-~/.config}/gh-pr-monitor/preferences.json`.
- All commands return structured JSON with consistent field names, no nulls, pre-joined thread replies.

## References

- [docs/SCHEMAS.md](../../docs/SCHEMAS.md) — JSON schemas
- [skills/references/USAGE.md](../references/USAGE.md) — Detailed usage
- [docs/CLAUDE_CODE.md](../../docs/CLAUDE_CODE.md) — Claude Code integration guide
- Fork of [agynio/gh-pr-review](https://github.com/agynio/gh-pr-review).
