---
issue: concept-role-template-enumerates-bundled-grants
kind: audit
category: other
artifacts:
  - concept:role-template
status: verified
opened: 2026-07-25T03:18:31Z
---

# The permissions doc inventories every bundled role, down to the grant strings

Rimsky's CLI ships six pre-built "role templates" — named permission bundles an operator selects in one step (`admin` grants everything, `read-only` grants only reads, and so on). The concept document describing role templates lists all six by name *and* spells out their exact underlying permission strings verbatim (`*`, `*:read`, `node:reset`, `message:send`, `instance:pause`…). The corpus rule for concept documents forbids exactly this second half: wire-level identifier strings are implementation inventory, owned by the compiled-in role files, and a doc that carries them must track every grant change forever. The phrase "six V1-bundled templates" adds a second, smaller violation — version-staged framing for a fact nothing suggests changes at v1.

The interesting line is between the two halves of the enumeration. The template *names* are what an operator actually types and selects — stable, user-facing identifiers, arguably the "kind of thing" a concept legitimately names (and the corpus does name individual permission strings elsewhere when one is structurally load-bearing — just never a whole catalog). The grant-string *literals* are unambiguously below altitude by the rule's own example list.

## Options

- **Shape-level only**: describe the range ("bundles spanning full access down to single-action grants"), defer both names and strings to the compiled-in files.
- **Keep the names, drop the strings** — operators keep their selectable-identifier reference; the wire inventory goes to code.
- Either way, **drop the "V1"** framing.

The ruling decides: full trim or names-stay, plus the V1 removal.

## Ruling

> Recommended ruling (/recommend-rulings): Keep the six template names
> as stable operator-facing identifiers; drop the exact grant-action
> literals (the *:read / node:reset strings) to the compiled-in role
> resources; drop the 'V1' from 'six V1-bundled'.
>
> Rationale: The names are what an operator selects — concept-
> appropriate kind-of-thing; the grant strings are wire-format
> identifiers squarely below altitude per the rule's own example list.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
