---
issue: claude-agent-operator-envs-not-service-namespaced
kind: audit
category: conflicting
artifacts:
  - decision:operator-env-namespaced-per-service
status: promoted
opened: 2026-08-16T08:48:09Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# Two claude-agent operator variables escape the per-service env namespace

Bundled services read operator configuration from environment variables that carry the service's own segment (RIMSKY_EXECUTOR_HTTP_NODE_…), so that in an all-in-one process every service's knobs are distinguishable from each other and from the core's; a decision fixes that and names a closed set of generic exemptions (host, ports, binary override, tags, timeouts, stub mode). Two claude-agent variables escape it: the dispatch spend cap carries no segment at all, and the observability bridge URL wears the generic executor prefix though only the claude-agent reads it — while http-node's equivalent knob is properly namespaced. The pin test regexes only host and port names, so neither is caught. The ruling renames them.

## Options

- Rename both to carry the claude-agent segment and widen the pin test to every operator variable outside the exempt set; cost: two variable renames operators must follow (pre-v1).
- Add a new exemption for these two; cost: no basis in the decision — a spend cap and a per-executor URL are not generic — and it recreates the inconsistency with http-node.

The ruling applies the namespacing rule to the two escapees.

## Ruling

> Generated ruling (/verify-issues): Rename the two variables to carry the claude-agent service segment, matching how the sibling executor names its bridge URL, and widen the pin test's regex from host/port to every operator variable outside the decision's exempt set so a future escapee is caught. Forced by the per-service namespacing decision, whose exempt set is closed and covers neither. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
