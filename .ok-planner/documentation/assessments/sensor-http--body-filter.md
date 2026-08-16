---
assessment: sensor-http--body-filter
subject: story:sensor-http
way: body-filter
release: d977250c
outcome: held
warrant: experiment:sensor-http
---
# Narrowing a watch so only the responses that matter become messages

The audit declared two watches on the same location through `catalog:bundled-services/sensor-http`: one unfiltered, one narrowed by a match on the response body. Across three successive bodies the narrowed watch sent exactly one message — the body satisfying its declared match — while the unfiltered watch on the same location had by then sent all three. The filter is therefore the operator's declaration and not an accident of what the upstream returned, and it composes with the change detection rather than replacing it.

## Unverified remainder

One filter form over one body shape was exercised. The demonstration does not establish how a filter that matches no body ever behaves over a long run, nor filtering on response metadata rather than the body.
