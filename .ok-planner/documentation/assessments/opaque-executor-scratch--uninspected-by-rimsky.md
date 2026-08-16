---
assessment: opaque-executor-scratch--uninspected-by-rimsky
subject: story:opaque-executor-scratch
way: uninspected-by-rimsky
release: d977250c
outcome: held
warrant: experiment:opaque-executor-scratch
---
# The bytes stay opaque — carried, never read or rewritten

The non-inspection half of the promise was taken as a count rather than an assurance. Across the forty-six records rimsky itself authored during the run, none carries any of the three byte strings in base64, hex or raw form. The park's own record notes the attachment's size and nothing more. An executor author can therefore treat the attachment as private in-flight state: rimsky moves it from one dispatch of a node-run to the next without reading it, reshaping it, or copying it into anything an operator can read.

## Unverified remainder

The count covered the records this run produced. It does not establish that no other surface of a deployment could expose the bytes, only that none of the records rimsky wrote during the run did.
