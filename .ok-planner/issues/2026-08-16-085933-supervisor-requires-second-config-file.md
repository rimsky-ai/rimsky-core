---
issue: supervisor-requires-second-config-file
kind: audit
category: conflicting
artifacts:
  - concept:rimsky-yml
status: verified
opened: 2026-08-16T08:59:33Z
---

# The supervisor requires a second configuration file that the single-file concept says does not exist

The rimsky.yml concept promises one configuration file consumed by every runtime process, with no per-process files. The supervisor role refuses to start without a second YAML named by its own environment variable, carrying concurrency, poll intervals and callback host/port/advertise-host; it is baked into the all-in-one image, written by both compose run paths, and mounted by the integration harness. A decision on launch config injection already concedes a separate supervisor-tuning file, but scopes itself to one verb's synthetic files; the second file's role has grown past that. The concept and the decision are in tension inside the corpus. The ruling decides whether the single-file promise is relaxed or restored.

## Options

- Amend the concept: the unified file carries deployment shape, and supervisor per-process tuning legitimately lives apart — naming the line between them; cost: ratifies the existing choice, the "single file" purity goes.
- Fold supervisor tuning into the unified file under a per-role section and take the advertise-host from the environment; cost: a real refactor that makes the invariant true again.

The ruling decides whether one file is a promise worth a refactor.

## Ruling

> Recommended ruling (/verify-issues): Restore the promise — fold the supervisor's tuning into the unified file under a per-role section, take the callback advertise-host from its existing environment variable, and retire the second file — updating the launch-injection decision to say the compose verb writes one synthetic file.
>
> Rationale: the second file exists as a scope-limited convenience the decision itself calls a smaller refactor deferred, and every consumer of it (image, compose paths, harness) is rimsky's own; a single file is the operator-facing promise the concept was written to make. Flip case: if per-supervisor tuning must differ across replicas of one deployment (different concurrency per host), a per-process file is the honest shape and the concept should say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
