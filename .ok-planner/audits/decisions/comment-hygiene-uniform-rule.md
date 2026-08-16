---
audit: comment-hygiene-uniform-rule
artifact: decision:comment-hygiene-uniform-rule
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 1639
unaccounted: 0
---

# Whether every surviving comment is a machine directive, a design citation, or a docstring in an opted-in file

Supported, measured rather than assumed. Scanning all 1,639 Go files outside the generated bindings and the suite estates — 5,587 comment lines in total — every comment block opens with one of the sanctioned forms: a licence or identifier header, a build or lint directive, a generated-file marker, or one of the design-citation tags. Exactly one apparent exception turned out to be fixture text inside a raw string literal in a licensing-check test, not a comment at all. Two files carry the docstring opt-in marker and hold documentation blocks, which is the third sanctioned form. The tag vocabulary the choice names is the configured one, and the lint that decides all of this runs on every edit through a blocking hook and over the whole tree in the test suite, so the rule is applied at the moment of writing rather than swept up later.
