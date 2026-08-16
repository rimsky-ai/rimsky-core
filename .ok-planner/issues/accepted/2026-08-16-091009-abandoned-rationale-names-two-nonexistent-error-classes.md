---
issue: abandoned-rationale-names-two-nonexistent-error-classes
kind: audit
category: conflicting
artifacts:
  - decision:terminal-error-abandoned-as-error-class
status: verified
opened: 2026-08-16T09:10:09Z
---

# The abandoned-signal decision's rationale names sibling error classes that do not exist

The decision that made "abandoned" an ordinary error class (so a downstream node subscribes to it like any other terminal error) argues its case by example: it lists three sibling classes the runtime synthesizes. Of the three, one names a watchdog the project retired by migration (`park-timeout`), one never existed (`handler-give-up` — giving up is an error-policy action, and a give-up settles with the executor's own class), and the third exists only under a different spelling (`instance_killed`, underscore, not hyphen). The Choice itself is fully carried; only the rationale's enumeration is wrong. The ruling decides the replacement.

A subscriber author using the rationale as a pattern guide writes subscriptions that never fire — the hyphenated spelling matches nothing on the wire.

## Options

- Replace the three examples with currently existing, correctly spelled synthesized classes; cost: the list can rot again when classes change.
- Drop the enumeration and make the uniformity argument from the taxonomy's shape ("every failure mode is a class under the error root"); cost: none, and it cannot rot.

The ruling decides which of two compliant rewrites the rationale carries.

## Ruling

> Generated ruling (/verify-issues): Rewrite the rationale to argue from the taxonomy's shape rather than by example — every runtime failure mode is a class under the error root and subscribes the same way — dropping the three named siblings; if an example is kept, it is a class that exists today in its wire spelling. The current-state-only rule forces the change: a design artifact may not cite classes the product does not emit. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
