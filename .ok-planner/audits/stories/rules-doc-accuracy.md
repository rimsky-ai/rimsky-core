---
audit: rules-doc-accuracy
artifact: story:rules-doc-accuracy
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:25:00Z
---

# Every path the contributor rules cite resolves, and the dead references stay out

Supported. Six checks across four legs, taken from the file a contributor
actually reads. Ten cited paths were in a filesystem-path shape and all ten
resolve against the checkout; the population is non-empty, so the check has
something to be wrong about. None of the four curated dead references appears in
the file. All four build targets the rules name are declared in the build file,
and the rules name the image-rebuild step the verification section depends on.
The rules file itself is tracked, so the file measured is the one every
contributor's checkout carries.

## Compliance

The body encodes the checking procedure rather than the need — "a path the rules cite in a recognized filesystem-path shape" and "a curated set of known-dead references never creeps back in" describe how the claim is verified, which belongs to the check, not the story.
The benefit clause is hedged past the point of being settleable — "is unlikely to hit an obviously missing surface"; compliant text states the outcome plainly, e.g. "so that I can act on the documented steps without stopping to hunt for a file that is not there".
