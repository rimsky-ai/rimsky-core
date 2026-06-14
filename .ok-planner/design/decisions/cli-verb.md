---
decision: cli-verb
status: adopted
---

# cli-verb

## Choice

`rimsky compose run <manifest>` is a sub-verb of the compose dispatcher, sibling to `up | down | plan | status`. The other four require a running rimsky reachable over the control-api; this one does not.

## Rationale

Sits naturally alongside the existing compose family. The verb operates on the same manifest format with the same engine; verb-naming consistency makes the surface scannable.
