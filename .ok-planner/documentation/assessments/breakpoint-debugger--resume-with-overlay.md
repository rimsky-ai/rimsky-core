---
assessment: breakpoint-debugger--resume-with-overlay
subject: story:breakpoint-debugger
way: resume-with-overlay
release: d977250c
outcome: held
warrant: experiment:breakpoint-debugger
---
# Resuming a stopped dispatch with an attribute changed

`catalog:http-routes/POST /v1/instances/{idOrKey}/breakpoints/{breakpoint_id}/resume` resumed the held hit carrying an attribute overlay, and reported it as the first resume of that hit. The re-fired dispatch settled carrying the overlaid value, and nothing the overlay did not name changed — the overlay is a targeted substitution into the dispatch, not a replacement of it. That is the debugging act the story is about: the operator changes one input and watches the same node run again against it.

## Unverified remainder

None: the passing run demonstrates the way as promised.
