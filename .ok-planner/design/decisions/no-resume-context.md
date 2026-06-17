---
decision: no-resume-context
status: as-is
aliases: []
---

# Resume context channel removed

## Choice

Remove `Park.payload`, `Park.session_token`. Remove the `ExecuteRequest.resume_context` field and the `ResumeContext` message. Executors that need state across a park-and-resume use `attributes_delta` to commit it at terminal and `ExecuteRequest.attributes` to read it on resume.

## Rationale

The resume-context channel duplicated what attribute carry-forward already provides. `decision:claude-agent-session-attribute` already shows the attribute path works for the canonical use case. Two parallel mechanisms violate the "one idiom per job" principle of the project's coding style.

## Alternatives

Keep resume context as the primary channel for Park-specific state — rejected because it is redundant with attribute carry-forward.
