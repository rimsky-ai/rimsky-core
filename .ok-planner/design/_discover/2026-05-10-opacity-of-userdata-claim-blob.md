---
topic: opacity-of-userdata-claim-blob
kind: invariant
---

# Opacity rule: userdata, claim payload/address/scope, blob bytes, event payloads are all inert in rimsky

## Description

Several byte streams flow through rimsky on behalf of someone else: userdata is opaque per-node config the template author attaches, claim payload/address/scope are producer-supplied byte streams, blob content holds spilled attribute/parked-payload/event-payload bytes, and named-event payloads are executor-emitted. Each could be parsed, logged for observability, hashed for deduplication, attached to traces, validated against a schema, or otherwise inspected. Doing any of that creates accidental coupling between rimsky and the carrier's semantics.

Four opacity rules enforce a uniform discipline:

- **Userdata opacity** (`modeling/attribute/substitution.go:15-19`). No substitution pass; no inspection; no validation beyond the executor's declared JSON Schema (which is executor-side, not rimsky-side).
- **Claim inertness** — claim payload, address, scope are opaque (`foundation/locks/types.go:93-101`). Rimsky reads them only via `walkPath` at the substitution leaf (`modeling/attribute/substitution.go:189-310`), via `stringifyRaw` for top-level address/scope directives (same file, around line 280), and at one sanctioned wire-encoding site (`foundation/integration/runner_dispatch.go:710-770`, `makeStoreHandle`).
- **Blob inertness** — blob content is opaque (`foundation/persistence/blob.go:25-50`). Same `walkPath` exception and the persistence-layer fetch on attribute/parked-payload/event read are the only read sites.
- Event payloads inherit the same discipline by extension (`foundation/integration/runner_named_events.go:20`, `foundation/persistence/node_events.go:16`).

Per-instance userdata overrides are validated only at their routing keys (`by_executor`/`by_node` plus the executor name and node name), never their fragment values (`modeling/controlapi/userdata_overrides.go:44-46`). The merge helper at `foundation/integration/userdata_overrides.go:36` is "shape-blind": rimsky inspects only the routing-keys, never the userdata fragments themselves.

Stated rationale in the docstrings: opaque bytes are forwarded verbatim to the executor (userdata), the substitution machinery (claim payload), or the executor again (event payload). Logging, formatting with `%v`, attaching to traces, or normalizing any of these would leak the carrier semantics into rimsky and undermine rimsky's project-agnostic substrate posture.

`docs/concepts/userdata.md` is the operator-facing rendering: "Rimsky never inspects, parses, substitutes, decrypts, hashes, indexes, pattern-matches, or otherwise acts on `userdata`. The bytes traverse Rimsky's address space unchanged. A `{{...}}` literal in `userdata` reaches the executor as a literal `{{...}}`. If you want substitution, use `attributes:`."

The price: rimsky cannot debug-print or dedupe these streams, and cannot offer "what was actually in there" tooling beyond the substitution leaf. Operators who want introspection use the executor's observability protocol (`GetTrace`) where supported, not rimsky's.

`docs/concepts/userdata.md` "Common mistakes" enumerates the four most common opacity confusions:

- Rimsky's userdata ≠ cloud-init userdata (cloud-init parses; rimsky doesn't).
- `{{...}}` in userdata doesn't substitute.
- Encryption is the operator's call; rimsky transports as bytes regardless.

## Code surface

- `modeling/attribute/substitution.go:15-40` — userdata-opacity + claim-inertness annotations.
- `foundation/locks/types.go:93-101` — claim-inertness annotation.
- `foundation/persistence/blob.go:25-50` — blob-inertness annotation.
- `foundation/integration/runner_named_events.go:20` — blob-inertness for event payloads.
- `foundation/persistence/node_events.go:16` — same.
- `foundation/integration/runner_dispatch.go:710-770` — `makeStoreHandle` (sanctioned wire-encoding exception).
- `foundation/integration/userdata_overrides.go:32-50` — shape-blind merge.
- `modeling/controlapi/userdata_overrides.go:44-46` — key-only validation.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §11, §20, §21 + "Non-obvious gotchas" ("Userdata is never substituted or inspected", "Claim content is inert in Rimsky", "Blob content is inert in Rimsky", "Event payloads are inert in rimsky").
- `docs/concepts/userdata.md` — operator-facing rendering.
- `docs/concepts/claim.md` — claim-side opacity.
- `docs/concepts/scope.md` — scope opacity.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — opacity in the foundation contract.

## Adjacent topics

- `2026-05-10-attribute-substitution-grammar` — substitution is the sanctioned introspection exception.
- `2026-05-10-blob-spill-pluggable-backends` — spill mechanism for large opaque bytes.
- `2026-05-10-userdata-overrides-by-instance` — overrides validate routing keys only.
- `2026-05-10-event-log-append-only-jsonb` — node_events inherit blob-inertness.

## Observations

- The userdata-opacity, claim-inertness, and blob-inertness rules are written as separate items but enforce a single discipline. CLAUDE.md "Non-obvious gotchas" repeats variations of the same rule four times (userdata, claim content, blob content, event payloads). The repetition is intentional — each byte stream has a distinct call-site set where the discipline could leak.
- The "sanctioned introspection sites" are `walkPath` (substitution leaf in `modeling/attribute/substitution.go`), `stringifyRaw` (same file, top-level address/scope), and `makeStoreHandle` (wire-encoding in `foundation/integration/runner_dispatch.go`). Three sites total. A reader expecting "exactly one sanctioned site" (which the substitution.go comment can suggest in casual reading) finds three.
- `userdata` is doubly-protected: the userdata-opacity rule keeps rimsky from inspecting; the substitution grammar (`2026-05-10-attribute-substitution-grammar`) doesn't include a `{{userdata.*}}` source kind, so even a misconfigured template can't ask rimsky to read userdata.
- Error messages near these byte streams cite path tokens only (`substitution.go:99-101`); this is part of the discipline. A grep for `slog.Any("payload"` or `fmt.Sprintf("...", claim.Payload)` should return zero hits across the supervisor code.
