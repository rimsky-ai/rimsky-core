---
issue: surface-intent-non-prefixed-env-vars
kind: audit
category: unclear
status: verified
opened: 2026-08-16T04:07:09Z
---

# The surface intent scopes public env vars to the RIMSKY prefix, but shipped code reads five others

The intent calls every RIMSKY-prefixed variable the shipped code reads public. The shipped code reads five variables outside the prefix: two vendor credentials the claude-agent executor forwards to its child, a colour-disabling convention the CLI honours, and the platform's HOME and PATH. The general rule reaches a consumer through the first three. The prefix rule excludes all five. The extractor defaulted all five internal. The ruling amends the intent.

## Options

- Name the vendor credentials and the colour convention as public exceptions and leave the platform variables out; cost: an exception list to maintain.
- Keep the rule to the prefix alone; cost: an operator who sets a vendor credential has no promise rimsky reads it.

The ruling decides how far "surface" reaches into third-party and platform variables.

## Ruling

> Recommended ruling (/verify-issues): Add the two vendor credentials and the colour convention to the intent as named public exceptions, and state that platform variables (HOME, PATH) are not surface.
>
> Rationale: an operator must set the credentials for the executor to work at all, which is the intent's own test for public. HOME and PATH belong to the platform, not to rimsky. Flip case: if rimsky ever routes the credentials through a RIMSKY-prefixed indirection, the exception list empties and the prefix rule stands alone.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
