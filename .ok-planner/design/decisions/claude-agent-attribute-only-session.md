---
decision: claude-agent-attribute-only-session
status: as-is
aliases: []
---

# claude-agent session token rides attributes only

## Choice

claude-agent writes `session_token: runId` to `attributes_delta` on every settling terminal (Success in the loop case, Park in the rate-limit case). On resume, the executor reads `req.attributes.session_token` and starts the CLI with its session-resume flag set to that token. The earlier dual-path code (Park-side resume context plus attribute carry-forward) collapses to the attribute branch only.

## Rationale

Required by `decision:no-resume-context` — `Park.session_token` and the `ExecuteRequest.resume_context` channel no longer exist. The attribute path was already wired and proven for the loop case; this extension covers the Park case under a single mechanism.

## Alternatives

None — required by `decision:no-resume-context`.
