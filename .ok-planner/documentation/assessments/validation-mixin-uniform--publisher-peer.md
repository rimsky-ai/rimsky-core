---
assessment: validation-mixin-uniform--publisher-peer
subject: story:validation-mixin-uniform
way: publisher-peer
release: d977250c
outcome: held
warrant: experiment:validation-mixin-uniform
---
# A service that plays the publisher role gets the same vetting

The same run declared a third peer as a publisher, and validating the template returned its finding too — called for the publisher role, naming the publisher it was called about, and neither peer being a claim producer. Both findings came back together from one template, so a service author gets the same vetting whatever role their service plays in the templates that use it. The findings also came back from `catalog:http-routes/POST /v1/templates`, so the mix-ins are consulted at registration and not only on the validate route.

## Unverified remainder

None: the passing run demonstrates the way as promised.
