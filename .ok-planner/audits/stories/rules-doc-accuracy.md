---
audit: rules-doc-accuracy
artifact: story:rules-doc-accuracy
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Cited paths in the contributor rules resolved against the checkout

Supported. The rules file a contributor reads is tracked by git, so it ships in
every checkout, and it was measured as it stands. All 10 of the tokens it
quotes in a filesystem-path shape resolve to a real artifact in the tree; the
population is every backtick-quoted token in the file, minus the Search Scoping
line, filtered by the shape rule this project recognises. None of the 4 curated
dead references appears anywhere in the file. All 4 make targets the file names
are declared in the Makefile, and it names the image-rebuild step its
verification section depends on.

## Compliance

The benefit clause is hedged where the outcome is observable: "unlikely to hit
an obviously missing surface" states a probability rather than something a
reader can settle by looking, and "I can trust that" names a state of mind
rather than a capability. Compliant text: "As a contributor following the
project's after-code-changes verification steps, I want every path and command
those steps name to exist in my checkout, so that I can follow them without
stopping to work out which reference has rotted."
