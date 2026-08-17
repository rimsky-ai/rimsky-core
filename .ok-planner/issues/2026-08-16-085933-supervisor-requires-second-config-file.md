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

The supervisor breaks the single-file promise. The rimsky.yml concept promises one configuration file that every runtime process consumes, with no per-process files. The supervisor role refuses to start without a second YAML file, named by its own environment variable, carrying concurrency, poll intervals and callback host, port and advertise-host. The all-in-one image bakes that file in, both compose run paths write it, and the integration harness mounts it. A decision on launch config injection concedes a separate supervisor-tuning file. That decision scopes itself to one verb's synthetic files. The second file's role has grown past that scope. The concept and the decision now conflict inside the corpus. The ruling decides whether the corpus relaxes the single-file promise or the code restores it.

## Options

- Amend the concept: the unified file carries deployment shape, supervisor per-process tuning lives apart, and the concept names the line between them; cost: ratifies the existing choice and drops the single-file promise.
- Fold supervisor tuning into the unified file under a per-role section, and take the advertise-host from the environment; cost: a refactor that makes the invariant true again.

The ruling decides whether one file is a promise worth a refactor.

## Ruling

> Recommended ruling (/verify-issues): Restore the promise. Fold the supervisor's tuning into the unified file under a per-role section, take the callback advertise-host from its existing environment variable, and retire the second file. Update the launch-injection decision to say the compose verb writes one synthetic file.
>
> Rationale: the decision itself calls the second file a smaller refactor deferred. Rimsky owns every consumer of the second file. Those consumers are the image, the compose paths, and the harness. The concept was written to promise operators one file. Flip case: if per-supervisor tuning must differ across replicas of one deployment, such as different concurrency per host, a per-process file is the honest shape and the concept should say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
