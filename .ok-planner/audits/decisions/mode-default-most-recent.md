---
audit: mode-default-most-recent
artifact: decision:mode-default-most-recent
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:52:00Z
---

# Four cascade modes, defaulting to most-recent, set once per node

Supported. The mode is a closed set of exactly the four named values, pinned three ways — a typed string constant set, a template-validation error naming all four when an author writes anything else, and a schema check constraint over the same four in both dialects. The default is most-recent in all three places it could be decided: the node column defaults to it, the accessor returns it for an empty stored value, and the rule dispatcher treats the empty mode as most-recent, so an unconfigured template gets the default whichever way it arrives. The four behaviours match their descriptions: most-recent drops the transitioning row when a later cascade pending exists and otherwise deletes prior cascade stales, giving the stated at-most-one bound; sequenced is a no-op; the two idempotent variants compare the canonical input bag against the prior queued round, with the settled variant additionally consulting the most recent settled predecessor when none is queued. The setting is per node and uniform across upstreams — it lives as one column on the node row and one optional template field, and the subscription declaration carries no mode, no per-sender key, and no per-signal-type key, so the rejected per-upstream alternative has no surface at all. Non-cascade immunity holds by construction: all four of the queries the walker rule and the mode rules use restrict to the cascade creation reason, and the rules only run on rows in the pending state, which non-cascade runs never occupy.
