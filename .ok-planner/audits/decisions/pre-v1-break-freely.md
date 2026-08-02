---
audit: pre-v1-break-freely
artifact: decision:pre-v1-break-freely
determination: supported
commit: b767a27d
audited: 2026-08-02T09:44:16Z
---

# No backwards-compat guarantee pre-v1; clean removal over shims

Supported. The persistence migration trees are forward-only and free of
compatibility shims: `lib/foundation/persistence/postgres/migrations/`
holds 28 sequentially-numbered `.sql` files with no paired "down" migration
of any kind, and several (e.g. `014-drop-supervisor-active-node-count.sql`,
`016-drop-wait-set-subscription-scope.sql`) drop columns/constraints for
retired functionality outright rather than carrying a compat column or a
dual-write path — `014`'s own header comment states the pre-v1 stance
explicitly ("safe only for a pre-v1 dev database dropped and recreated...
Post-v1, ordinals are never reused"). A repo-wide grep of hand-written `.go`
source (excluding the generated `lib/protocols/proto/v1/gen/` tree, whose
`// Deprecated:` lines are protoc-gen-go reflection boilerplate, not
project-authored compat code) for backward-compatibility vocabulary
("legacy", "shim", "backward(s)-compat", "kept/retained for compat") and for
version-branching patterns ("compat mode", "older schema version") found no
hand-rolled compatibility path anywhere in `cmd/` or `lib/`.
