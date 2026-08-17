---
issue: remote-run-diverges-from-exit-code-classes
kind: audit
category: conflicting
artifacts:
  - decision:exit-codes
status: promoted
opened: 2026-08-16T08:48:04Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The remote one-shot run does not report the exit-code classes the decision fixes

The exit-code decision fixes four outcome classes for run-to-terminal verbs (success 0, any failure 1, timeout 2, signal 130), and the compose one-shot and self-hosted ephemeral run implement them through a shared shutdown coordinator. The remote ephemeral run does its own thing: it returns 1 on timeout, 0 on interrupt (a test even pins that as "clean interrupt exit"), and 0 after the wait regardless of whether the instance succeeded or failed — it never reads the outcome. A script driving the remote verb cannot tell a failed run from a successful one, which is the promise the script-friendly-outcome story makes with no verb exception. The ruling routes the remote path through the shared classification.

## Options

- Route the remote path's terminal handling through the same classification and mapping the coordinator implements — timeout 2, interrupt 130, read the instance outcome and return 1 on failure — and replace the pinned test; cost: none beyond the change.
- Amend the decision to exempt the remote path; cost: contradicts the story's unqualified promise with no rationale on record.

The ruling makes the four classes hold on every run-to-terminal verb.

## Ruling

> Generated ruling (/verify-issues): Give the remote run the same outcome classification as the other two verbs — timeout exits 2, interrupt exits 130, and after the wait the instance's actual outcome decides between 0 and 1 — using the shared mapping the compose coordinator already carries, and replace the test that pins 0 on interrupt. Forced by the exit-code decision (no verb carve-out) and the script-friendly-outcome story; the shared machinery already exists. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
