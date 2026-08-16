---
audit: coverage-wildcard-asymmetry
artifact: decision:coverage-wildcard-asymmetry
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T09:35:00Z
---

# Wildcard subscriptions cover per-field reads while per-field subscriptions do not cover whole-pull reads

Supported. The registration-time coverage matcher decides all four sender-reader combinations for attribute reads, and each behaves as the decision states. A whole-pull read — an attribute reference with no field path — is satisfied only by the wildcard subscription and is reported uncovered otherwise, with the wildcard named as the suggested entry. A per-field read is satisfied by its exact per-field subscription and, failing that, by the wildcard, which is the asymmetry's other half; the wildcard is a legal subscription type under the signal taxonomy's trailing-wildcard rule, so that branch is reachable rather than dead. A per-field subscription leaves a whole-pull read uncovered. The message-side counterpart of the same matcher carries the analogous pair and is separately exercised. The earlier verdict rested on the wildcard-covers-per-field arm having no test in any suite and no template, fixture, or scenario pairing a wildcard attribute subscription with a per-field read — which is true, and remains a gap in what the suite exercises — but the decision claims a behaviour of the product rather than a test obligation, and the behaviour is present and correct in the matcher. Unexercised is not unsupported.
