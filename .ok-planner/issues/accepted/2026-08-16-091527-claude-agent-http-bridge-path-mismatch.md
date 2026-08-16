---
issue: claude-agent-http-bridge-path-mismatch
kind: audit
category: conflicting
artifacts:
  - decision:http-bridge-preserved
  - concept:executor
status: verified
opened: 2026-08-16T09:15:27Z
---

# The claude-agent's HTTP bridge serves a path rimsky's own HTTP executor client never dials

Two bundled executors expose an HTTP-JSON bridge beside gRPC, and a decision preserves it so callers that dispatch over HTTP keep a working surface. The supervisor's HTTP executor client posts to the versioned execute path; the http-node bridge serves exactly that; the claude-agent bridge serves an unversioned lower-case path instead — so a claude-agent registered with the HTTP transport 404s on every dispatch, and its README repeats the wrong path. The two shipped bridges disagree with each other and one disagrees with rimsky. The ruling aligns the path.

## Options

- Change the claude-agent bridge to serve the versioned path the sibling bridge and the client agree on, and correct its README; cost: none beyond the change.
- Amend the decision to say external callers only; cost: contradicts the decision's own rationale — rimsky is such a caller.

The ruling makes the bridge reachable.

## Ruling

> Generated ruling (/verify-issues): Serve the claude-agent's HTTP bridge on the same versioned execute path the http-node bridge serves and the supervisor's HTTP client dials, and correct the executor's README. Forced by the bridge-preserved decision's purpose (a working HTTP-JSON surface for HTTP-JSON callers) and the one-idiom-per-job rule. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
