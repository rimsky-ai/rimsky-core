---
assessment: producer-error-passthrough--read-producer-error
subject: story:producer-error-passthrough
way: read-producer-error
release: d977250c
outcome: held
warrant: experiment:producer-error-passthrough
---
# Reading a failing producer's own class and message in what the operation returns

The audit wired a released deployment to two claim producers built against the published producer protocols, each refusing its release verb with a different declared error class. Retiring the asset each producer holds through `catalog:http-routes/DELETE /v1/instances/{id}/assets/{alias}` failed rather than reporting success, and each failure came back carrying the producer's own error class, the producer's own message naming the claim it refused to drop, the name of the producer that failed, and the verb it was running. The two producers yielded two different classes, so the class follows the producer rather than being a rimsky constant. The status distinguished a producer rejection from an internal error. `catalog:cli-verbs/rimsky asset delete` exited non-zero and repeated the producer's message, so the operator gets the same detail from either surface, and both assets remained in place, matching the refusal. Fifteen checks, none failing.

## Unverified remainder

The passthrough was measured on the release verb of two claim producers. The way does not enumerate every producer verb, nor the store-side producers the story also names.
