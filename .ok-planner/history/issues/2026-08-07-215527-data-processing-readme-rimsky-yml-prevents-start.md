---
issue: data-processing-readme-rimsky-yml-prevents-start
kind: human
category: doc-drift
artifacts:
  - concept:data-processing
  - concept:rimsky-yml
  - story:data-processing-author
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:27Z
github: https://github.com/rimsky-ai/rimsky-core/issues/104
---

# Following the data-processing example's config takes the control plane down

The data-processing example ships a configuration snippet showing how to point
rimsky at it. Applying that snippet stops rimsky from booting.

The cause is a protocol mismatch the snippet papers over. Data processing is an
optional add-on protocol that rides on a claim producer — it is not a service you
can register on its own. The snippet registers the example under the
claim-producers section. At startup rimsky dials every entry there and calls the
claim-producer capability check; the example implements only the data-processing
service, so the call comes back unimplemented and startup fails hard.

This is the most severe defect in the current examples batch, because the failure
mode isn't a broken example — it's a control plane that won't start. The
README's own prose, immediately above the snippet, states the mix-in relationship
correctly. Only the snippet is wrong.

Two smaller claims in the same file were re-verified and are false. The README
says rimsky cross-checks a template's data declarations against the capabilities
the producer advertises; nothing does — the declaration is opaque bytes end to
end, and the capability envelope is read only by the conformance runner. And it
names a package for the registry construction that doesn't exist.

## Options

The two smaller corrections aren't in question. The snippet has two honest fixes:

- **Give the example a minimal companion claim producer**, so it can be
  registered exactly as the README shows. This is the pattern the validation
  example already uses to demonstrate its own mix-in. Costs a small amount of
  code in a file whose point is to be small, and keeps the walkthrough
  copy-pasteable.
- **Rewrite the snippet and prose** to say the example must be fronted by a real
  claim producer, pointing at the validation example for the pattern. No new
  code, but the "point rimsky at the producer" walkthrough stops being something
  a reader can follow end to end.

The ruling decides whether this example stands alone or documents its dependency.

## Ruling

> Retired: the examples module is being removed in full, so the README this issue
> reports on ceases to exist and the drift it documents is dissolved rather than
> corrected. The documentation project maintains the cookbook that replaces it.
>
> Findings underneath these issues that concern rimsky rather than the examples
> were pulled out as their own issues before retirement — an unenforced publisher
> kind, an unproven ordering guarantee on terminal events, and a lifecycle
> callback that cannot refuse — so nothing about the platform is lost with the
> module.
