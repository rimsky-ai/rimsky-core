---
issue: concept-role-template-enumerates-bundled-grants
kind: audit
category: other
artifacts:
  - concept:role-template
status: repaired
opened: 2026-07-25T03:18:31Z
---

Question: does the role-template concept's listing of all six bundled role names plus their exact grant-string literals violate the concept-altitude rule, and does either half survive — names, strings, both, neither?

Rule that determined the fix: `SELF-CONTAINMENT-RULE` names "wire-format identifiers" as a forbidden concept-body enumeration — the grant-action literals (`*:read`, `node:reset`, ...) match that example exactly, forcing their removal. The template *names* are what an operator selects and types, not an implementation detail of a wire protocol — they are the small, closed, structurally-fixed population the concept is defined over, the same status `concept:conformance`'s per-protocol scenario categories and `concept:signal`'s named leaves hold — so the rule does not reach them. Separately, `CURRENT-STATE-ONLY-RULE` bars version-staged framing ("six V1-bundled templates") for a fact nothing suggests changes at v1.

What changed: `.ok-planner/design/concepts/role-template.md` — kept the six template names (`admin`, `operator`, `read-only`, `agent-supervisor`, `publisher-service`, `debug-operator`) as a flat list with a one-clause shape summary ("spanning full platform access down to a single-action grant") in place of the per-role grant-string breakdown; added the deferral sentence "The exact grant strings each name expands to are owned by the compiled-in role files, not enumerated here."; dropped "V1" from "six V1-bundled templates."

How verified: re-read the file — no grant-action literal (`*`, `*:read`, `node:reset`, etc.) remains in the body; the six names and the Boundaries/Invariants sections (unaffected) are consistent with the new "What it is" text. Markdown-only change, no build/test impact.
