---
decision: claude-agent-attribute-only-session
status: as-is
aliases: []
---

# claude-agent session token rides attributes on Success, scratch on Park

## Choice

claude-agent's session token rides two channels depending on which settling terminal carries it. On Success (the loop case), the executor writes `session_token: runId` to `attributes_delta`, which reaches the next dispatch through the standard attribute carry-forward. On Park (the rate-limit resume case), which cannot carry `attributes_delta`, the executor instead writes the token into scratch, base64-encoded on the Park scratch slot; since the parked row is the same row that re-dispatches at resume, that scratch value is read back verbatim. On dispatch the executor reads the session token from scratch first, falling back to the carried-forward attribute when scratch is empty, so one read covers both a park-resume and an ordinary continuation. The earlier dedicated Park-side resume-context channel no longer exists; Park's share of session continuity now rides scratch instead.

## Rationale

Required by `decision:no-resume-context` — `Park.session_token` and the `ExecuteRequest.resume_context` channel no longer exist, and Park does not carry `attributes_delta`, so Park's session-token leg has to move to a channel Park does carry. Scratch is the channel purpose-built for opaque executor-private state that must cross the park boundary. Success keeps using `attributes_delta` because it already carries the token reliably through the normal carry-forward path and there is no boundary forcing it off that channel.

## Alternatives

Routing both cases through `attributes_delta` — rejected because Park does not carry `attributes_delta`; the Park leg has no attribute channel to use, so it must move to scratch regardless of what Success does.
