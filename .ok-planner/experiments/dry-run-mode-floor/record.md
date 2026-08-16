---
experiment: dry-run-mode-floor
commit: PENDING
---

# An api-key whose grant pins a write to dry-run

## What it ran against

A `rimsky-all-in-one` container from this tree's image set with authentication
enabled, and three keys minted through `auth create-key --role-file`: one
granting `tag:create` with `"mode": "dry_run"`, one granting `tag:create`
unpinned, and one holding both the pinned grant and an unpinned `tag:*`. Re-run
unchanged at this tree.

## What was observed

The pinned key's plain `POST /v1/tags` — no flag on the request at all — came
back as the synthetic envelope `{"dry_run": true, "would_have_created_tag":
{...}}`, and the tag was absent from the store afterwards. Repeating it with
`?dry_run=false` produced the same envelope and the same absence, so the holder
cannot escalate its own credential. The unpinned control key performed the real
write and its tag persisted. The mixed key also performed the real write, which
is the story's stated proviso: the floor holds only while no other grant
authorizes execute-mode on the same action. The pinned key kept its read grant
throughout.

RESULT: PASS
