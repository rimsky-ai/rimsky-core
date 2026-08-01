# Intent Dossier: terminal-tag

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.
All entries for this concept are transcript-tier (the concept was born 2026-06-16).

## Net position

- Terminal tags **replace named events** (2026-06-16, user): every settling terminal is exactly a type (success | error | park) plus attributes_delta plus a **set** of tags, all committed at once at terminal time. Tags being a set makes multi-emit structurally idempotent.
- **Tags are ephemeral, attributes persist** (2026-06-24, user): tags are emission-scoped metadata never persisted into node state — "tags just exist as a way to *not* persist additional data from an executor." The concept doc must state this distinction explicitly.
- Subscription routing over tags is CEL: the canonical replacement for `event/<name>` is a `terminal/*` subscription with `when: "<tag>" in payload.tags`; TerminalSuccessPayload and TerminalErrorPayload carry `Tags []string` so predicates can see them.
- Tag vocabulary is declared and two-gated: executors declare `declared_tags` (renamed from `declared_events`) in ObservabilityCapabilities; gate-1 at template registration checks CEL tag literals against the emitter's declared_tags (warning on undeclared per the landed shape); gate-2 at runtime rejects any settling outcome carrying an undeclared tag as an executor protocol violation.
- Park outcomes carry tags on the wire and in the audit log for observability, even though park signals never cascade-fire; park's attributes_delta was backed out (park and infra reclassified as non-terminals).
- The whole transition was ratified as a **refactor/simplification with no user-observable capability change** — one signal grammar (terminal/* + tags) instead of three overlapping mechanisms.

## Required behaviors (open promises)

- Uniform settling shape: attributes_delta + tags + scratch on every executor settling terminal; invariant terminal-atomic-commit — attributes_delta applied inside the same caller-provided transaction before the error-policy chain or park mutation runs, at both the terminal-error and terminal-park upsert sites (2026-06-17, b31002b8, transcript).
- Two-gate tag enforcement, with the runtime gate explicitly ratified as a plan step "so it would not be silently dropped" (2026-06-16, 055468fc, transcript); undeclared tag on an outcome → `executor_protocol_violation` (2026-06-17, b31002b8).
- Cascade-firing signal set post-collapse: `terminal/success`, `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed` — tag-conditional firing via terminal/* + CEL over payload.tags; per-sender, cross-cutting (instance: true), audit-row landing, and trailing-* matchers all apply over this set (2026-06-17, b31002b8, transcript).
- Wait-set `topic_kind` CHECK is the 3-value taxonomy (no 'event') (2026-06-17, b31002b8).
- Built-in loop_counter executor declares exactly two tags ('loop', 'done'); Success carries 'loop' below max, 'done' at max; with max=3 it dispatches three times (two loop, one done); self-subscription is `terminal/success` with `when: "loop" in payload.tags` (2026-06-17, b31002b8, transcript).
- Park outcomes keep tags for audit-row observability (2026-06-24, 8a8539a4, transcript).
- Per-emission data rides `nodes.<X>.attribute.<key>` — attributes, not tags, are the data channel (2026-06-17, b31002b8).

## Intentional absences

- **NamedEvent protocol message, the `rimsky_node_events` ledger table (dropped, migration 013), the `event/<name>` signal type-path, and the `nodes.<emitter>.event.<name>` substitution path** — all removed with the named-event concept retired to _retired/; terminal-tag is the successor (2026-06-16/17, user-ordered).
- **Tag persistence into node state** — tags are deliberately ephemeral (2026-06-24, user).
- **attributes_delta on park/infra signals** — the 2026-06-24 "attributes_delta on ALL event classes" uniform guarantee was deliberately narrowed the same day: park and infra were reclassified as non-terminals and excluded; the park backout was scoped to attributes_delta only (tags stayed).
- **`AsyncCallbackBody.events`** — removed with the named-event retirement (2026-06-16).

## Corrections and restorations (drift-fight record)

- None beyond the deliberate same-day narrowing above; the concept's history is a clean design replacement, not a drift fight. Adjudication note: any finding that expects the rimsky_node_events table, event/<name> subscriptions, event substitution paths, or `declared_events` asserts pre-2026-06-16 expectations — those removals are by design.

## Superseded / historical

- Named events (NamedEvent stream emissions, events ledger, event/<name> signal leaf, event substitution) → tags on terminals (2026-06-16/17).
- `declared_events` → `declared_tags` (2026-06-16).
- "attributes_delta on all event classes" (2026-06-24 morning) → narrowed to settling terminals only, park/infra excluded (2026-06-24, same day).
