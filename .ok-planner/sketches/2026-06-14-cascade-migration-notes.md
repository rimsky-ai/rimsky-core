# Cascade-migration notes

Captures the one-shot migration mechanics for the explicit-substitution / cascade-behavior change. Migration is transitional — the durable design lives in `concept:attribute`, `concept:node-subscription`, and the `subscription-edges-only-from-explicit-block` / `hard-dep-field-no-special-case` decisions. These notes record the migration tool's behavior only.

## Existing subscriptions get today-equivalent flag values

Every existing `subscribes:` entry in the codebase receives `wake_on_change: true` and `force_upstream_refresh: false` at migration time.

Rationale: the migration preserves prior behavior on every subscription verbatim; the migration's correctness is established by every existing test continuing to pass.

## Attribute-field `hard_dep: true` flags become `force_upstream_refresh: true` subscriptions

Every attribute-field `hard_dep: true` flag is removed at migration time, and the corresponding receiver's subscription entry for that sender carries `force_upstream_refresh: true`. If no existing subscription names the sender, a new entry is added with `wake_on_change: true` and `force_upstream_refresh: true`.

Rationale: preserves the prior hard-dep runtime behavior exactly; the existing hard-dep tests carry over as regression coverage for the new flag.

## Implicit edges become explicit subscriptions at migration

Every implicit edge created by a substitution ref without a matching explicit subscription is replaced at migration time by an explicit subscription entry with today-equivalent flags (`wake_on_change: true`, `force_upstream_refresh: false`).

Rationale: preserves prior behavior on every implicit edge verbatim. After migration, no template relies on implicit edges; the registration coverage check catches new instances at registration.
