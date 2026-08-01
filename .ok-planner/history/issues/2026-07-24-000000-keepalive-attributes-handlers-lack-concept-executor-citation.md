---
issue: keepalive-attributes-handlers-lack-concept-executor-citation
kind: human
category: citation-coverage
artifacts:
  - code:lib/runtime/keepalive.go
  - code:lib/runtime/attribute_writeback.go
  - concept:executor
status: repaired
opened: 2026-07-24T00:00:00Z
---

# The code enforcing a security rule didn't point back to the document that states it

Question: should the code enforcing the incremental-callback bearer-token check (keepalive and attribute-writeback) carry a `@concept:executor` citation, and if so, where?

Rule that determined the fix: `.ok-planner/CLAUDE.md`'s annotation convention — "any time you consult a concept... to understand or modify a file, leave `@concept:`... at the most-specific load-bearing site... the function, branch, or block where the artifact's commitment is actually enforced" — and MECHANICAL-VS-JUDGMENT-RULE names a missing annotation as a standard mechanical code-side repair. Re-reading the code: both `handleKeepalive` (`lib/runtime/keepalive.go`) and `handleAttributeWriteback` (`lib/runtime/attribute_writeback.go`) call one shared, previously-uncited helper, `authorizeCancelToken` (`lib/runtime/keepalive.go`), which is where the bearer-token check is actually enforced — confirming the filed Problem's placement analysis still holds. The apparent precedent citing `concept:executor` three times in `lib/runtime/callback.go` was re-checked and confirmed unrelated: those three citations sit on the async-callback outcome's `Scratch` field, not on any auth check. `concept:supervisor`'s own prose was also re-checked and, unlike the filed Problem assumed, already states the keepalive/attribute-writeback bearer-token requirement correctly and completely — no matching gap remained there to fix.

What changed: added `// @concept: executor` directly above `authorizeCancelToken` in `lib/runtime/keepalive.go` — the one shared enforcement site both handlers call — rather than duplicating the tag on both handlers.

How verified: `go build ./lib/runtime/...` passes; the added line is a comment only, no behavior change.
