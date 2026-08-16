---
assessment: subscription-mounting--failed-mount-visible
subject: story:subscription-mounting
way: failed-mount-visible
release: d977250c
outcome: held
warrant: experiment:subscription-mounting
---
# A create that succeeded but whose publisher is missing shows as a failed subscription

The audit created a second instance whose declared publisher this deployment does not run. The create through `catalog:http-routes/POST /v1/instances` succeeded and returned an instance id — carrying no sign of the problem — while the instance's subscription reported the failed state with the reason that the publisher is not in the deployment's registry. This is the silent create the story exists to catch: the failure is visible on the subscription, where the operator is already looking, rather than hidden behind a successful response.

## Unverified remainder

One failure reason — an unregistered publisher — was exercised. The demonstration does not establish what a subscription reports when the publisher exists but is unreachable.
