---
issue: egress-allowlists-invert-allowlist-default-polarity
kind: audit
category: conflicting
artifacts:
  - decision:allowlist-defaults-open
status: verified
opened: 2026-08-16T08:48:01Z
---

# One decision says every bundled-service allowlist defaults open; the two egress allowlists default closed

The decision on allowlist defaults says an unset operator allowlist accepts every reference a template makes. That holds for the claude-agent executor's two allowlists, MCP servers and exposed env. It does not hold for the two egress allowlists: the SSRF guard on the http-node executor and the guard on the sensor-http sensor. Those two default closed for private, loopback and metadata destinations. They do so deliberately, and the project's own guidance and the sensor concept say so. The decision's unqualified Choice is the only wrong text. The behaviour is right. The ruling decides how many decisions carry the two polarities.

A reader consulting only that decision, the one that names these variables, concludes an unset egress allowlist accepts every destination. The guard does the opposite.

## Options

- Narrow the decision to the reference-allowlist class it governs and add a companion decision for the destination allowlists' closed default, mirroring the webhook-auth decision that owns an analogous fail-closed boundary; cost: one more artifact.
- Restate the one decision with both polarities and the reason they differ; cost: one artifact carrying two opposite defaults.

The ruling decides the artifact shape. The behaviour stays.

## Ruling

> Recommended ruling (/verify-issues): Split them. Narrow the existing decision to reference allowlists, which default open, and record a companion decision for destination allowlists, which default closed and take private-range egress by opt-in. Each polarity then has one home with its own rationale.
>
> Rationale: the corpus already keeps one decision per boundary posture, and webhook auth has its own. A single decision stating opposite defaults for same-suffixed variables invites the very confusion the run measured. Flip case: if the two variable families are renamed so their suffixes differ, one decision can carry both without ambiguity.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
