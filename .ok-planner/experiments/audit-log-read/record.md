---
experiment: audit-log-read
commit: PENDING
---

# Operator reads the auth-relevant action audit

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. The run provokes
each action the story names — key creates, a revoke, a rotate, an executed write,
a dry-run write, a permission denial, a no-token denial and an invalid-token
denial — using the `rimsky auth` verbs and the control API, then reads them back
through `GET /v1/audit` and exercises every filter that route accepts. Re-run
unchanged at this tree.

## What was observed

The audit log carried all five record kinds: 4 `auth.key_created`, 1
`auth.key_revoked`, 1 `auth.key_rotated`, 9 `auth.access_attempted`, 3
`auth.access_denied`. The records carried what they name: each minted key by
name, the revoked and rotated keys by name, the dry-run write recorded with mode
`dry_run` and `executed: false` against the executed write's `execute` and
`true`, and the three denials distinguished by reason (`invalid_token`,
`no_token`, `permission_denied`).

Every filter narrowed as documented: `kind` (and a 400 for a kind outside the
auth allowlist), `key_name`, `action`, `action_prefix`, `target`, `status`,
`mode`, `since` (and a 400 for a non-RFC3339 value), and `limit` with a
`next_cursor` that paged to a different record. Reading the log is itself gated:
a `read-only` key got 200 and a key without `audit:read` got 403.

EXPERIMENT PASS
