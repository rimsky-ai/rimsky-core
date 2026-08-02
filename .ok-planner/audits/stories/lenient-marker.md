---
audit: lenient-marker
artifact: story:lenient-marker
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:14Z
---

# Lenient `?` substitution marker resolves a missing source to empty

Supported. `lib/graph/attribute/substitution.go::ParseLenientMarker` and `ParseDirectiveShape` parse a trailing `?` on a directive body; `resolveDirectiveValue` returns `nil, nil` (rather than propagating the resolution error) when the body is marked lenient and the source is absent, and a non-lenient directive over the same absent source still errors. An end-to-end scenario test, `test/scenarios/attributes/lenient_marker_recovery_test.go`, dispatches one node whose attribute source uses `{{nodes.upstream.attribute.maybe?}}` against an upstream that never sets `maybe`, and asserts the dispatched attribute resolves to the empty string, while a sibling node using the same source without the marker is asserted to fail dispatch (`template_resolution_failed`, node never reaches a clean terminal). Unit coverage in `lib/graph/attribute/substitution_test.go` and `lib/graph/node/template_validator_attribute_source_test.go` also exercises the marker's interaction with template validation and the `? | fallback` mutual-exclusion rule.
