# Test Plan for gh-pr-monitor Extension

## What to Look For

- Commands execute without errors
- JSON output is valid
- Field names use snake_case (e.g., `thread_id`, `is_resolved`)
- GraphQL queries have no unused variables
- Exit codes are correct

## Create Private Test Repo

```bash
gh repo create <username>/gh-pr-monitor-test --private --description "Test repo for gh-pr-monitor extension"
cd /tmp
git clone git@github.com:<username>/gh-pr-monitor-test.git
cd gh-pr-monitor-test
echo "# Test Repo" > README.md
git add .
git commit -m "Initial commit"
git push
git checkout -b test-branch
echo "test change" >> README.md
git add .
git commit -m "Test change"
git push -u origin test-branch
gh pr create --title "Test PR" --body "Test PR for smoke testing gh-pr-monitor"
```

## Delete Test Repo

```bash
gh auth refresh -h github.com -s delete_repo
gh repo delete <username>/gh-pr-monitor-test --yes
```

## Smoke Testing Commands

```bash
gh extension install elecnix/gh-pr-monitor
cd /tmp/gh-pr-monitor-test
gh pr-monitor review view --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor review --start --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor review --add-comment --repo <username>/gh-pr-monitor-test --pr 1 --review-id <PRR_ID> --path README.md --line 1 --body "Test comment"
gh pr-monitor review --edit-comment --repo <username>/gh-pr-monitor-test --pr 1 --comment-id <PRRC_ID> --body "Edited comment"
gh pr-monitor review --delete-comment --repo <username>/gh-pr-monitor-test --pr 1 --comment-id <PRRC_ID>
gh pr-monitor comments reply --repo <username>/gh-pr-monitor-test --pr 1 --thread-id <PRRT_ID> --body "Test reply"
gh pr-monitor threads list --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor threads view <PRRT_ID>
gh pr-monitor threads resolve --thread-id <PRRT_ID> --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor threads unresolve --thread-id <PRRT_ID> --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor react <PRRC_ID> --type thumbs_up
gh pr-monitor monitor --once --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor review --submit --repo <username>/gh-pr-monitor-test --pr 1 --review-id <PRR_ID> --event COMMENT --body "Test review submission"

# Draft Management Commands
gh pr-monitor draft status --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor draft mark --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor draft status --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor draft ready --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor draft status --repo <username>/gh-pr-monitor-test --pr 1
gh pr-monitor draft list --repo <username>/gh-pr-monitor-test
```

## Expected Output Format

### Thread Output

```json
{
  "thread_id": "PRRT_...",
  "is_resolved": false,
  "updated_at": "2026-04-08T01:51:27Z",
  "path": "README.md",
  "line": 1,
  "is_outdated": false
}
```

### Draft Status Output

```json
{
  "pr_number": 1,
  "is_draft": false,
  "title": "Test PR"
}
```

### Draft Action Output

```json
{
  "pr_number": 1,
  "is_draft": true,
  "status": "marked as draft"
}
```

### Draft List Output

```json
[
  {
    "pr_number": 1,
    "is_draft": true,
    "title": "Draft PR 1"
  },
  {
    "pr_number": 2,
    "is_draft": true,
    "title": "Draft PR 2"
  }
]
```
