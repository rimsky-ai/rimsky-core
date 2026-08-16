---
audit: validation-warnings-surfaced
artifact: story:validation-warnings-surfaced
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:42:46Z
---

# A template author sees the static validator's advisories, and can make them blocking

Supported. Driven through the public surface against a container of the
released all-in-one image with a template that trips exactly one advisory — an
error-class policy naming a class no executor and no producer declares — and
nothing else. Five legs, thirteen checks, none failing: validation answered ok
and carried the advisory; validation with the promotion flag answered not-ok
and still named the advisory that flipped it; registration succeeded and
carried the advisory; registration with the promotion flag was rejected, echoed
the flag, named the advisory and persisted no template, with the catalog count
unchanged either side. The same two paths through the operator CLI printed the
advisory and flipped their verdict under the flag. One gap observed: a
successful registration through the CLI without the flag prints only the
template id, dropping the advisories the response it read carried — the author
reaching for them through the lint path or the response itself still gets them.

## Compliance

- The body prescribes mechanism — "promote them to errors with the existing flag" names a flag, an implementation choice decisions own, and "existing" is build-record language a durable story does not carry; the compliant capability is "make those advisories blocking".
- The body names the delivery surface — "in the registration and validation responses" is where the product exposes the advisory, which belongs in decisions, not the story.
- The benefit clause is framed on the implementation — "so advice the validator already computes reaches me" states what the product internally computes rather than what the author can then do; the compliant benefit is that the author fixes questionable declarations before running the template instead of discovering them at run time.
