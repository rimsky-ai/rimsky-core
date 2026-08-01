---
issue: concept-event-log-enumerates-auth-kind-payloads
kind: audit
category: other
artifacts:
  - concept:event-log
status: repaired
opened: 2026-07-25T03:18:31Z
---

Question: does the event-log concept's "Auth event kinds" section — naming all five `auth.*` wire strings and every payload field verbatim — violate the concept-altitude rule against wire-format enumeration, and if so, where does the detail belong: deferred, exempted, or relocated to the sibling decision?

Rule that determined the fix: `SELF-CONTAINMENT-RULE` bars "wire-format enumeration" from concept bodies; the filed "relocate to `decision:event-log-payload-shapes`" option is foreclosed by `DECISION-DEFINITION`'s own bar on decisions being specs ("does not enumerate implementation steps, schema details ... call sequences") — a field-by-field payload listing has no licensed home in either catalog, so the corpus and its rules force the "defer" reading, not relocation. The corpus's own precedent for a small, genuinely closed vocabulary (`concept:signal`'s named `transient/park` / `transient/await_async` leaves) licenses naming the five kind *names* — a small, closed, structurally-fixed set the issue itself calls "deliberately closed" — while deferring per-kind field membership to the emission code, exactly as `concept:signal` defers payload field membership.

What changed: `.ok-planner/design/concepts/event-log.md` — the "Auth event kinds" section now names the five kinds by role (attempted access, denied access, key creation, key revocation, key rotation — dropping the literal wire strings, which are themselves implementation identifiers) and states the shared actor/action/target/result/mode payload shape plus the deferral sentence ("payload field membership is owned by the emission code, not enumerated here"), replacing the five bulleted field-by-field payload listings. Preserved the two behavioral facts in that old text that are genuine invariants, not field inventory, by moving them into `## Invariants`: key expiry never itself emits `auth.key_revoked` (revocation is explicit/rotation-grace-driven only), and `auth.key_rotated` names the new/surviving key as its actor fields (so the audit-read filter treats it uniformly with other auth rows).

How verified: re-read the file; every remaining sentence in "Auth event kinds" states a property of the kind vocabulary or a cross-kind shared shape, no per-kind field lists remain. Markdown-only change, no build/test impact.
