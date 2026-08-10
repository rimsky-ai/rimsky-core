---
audit: lenient-marker
artifact: story:lenient-marker
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A lenient substitution directive resolves to empty instead of failing the dispatch

Supported. Against an all-in-one deployment driven through the control API, two
templates differed only in the lenient marker on one directive, and in both the
upstream the directive reads could not run. Without the marker the reading node
settled `terminal/error/template_resolution_failed` and the error named the
directive it could not resolve. With the marker the same node dispatched and
settled successfully, its resolved bag carrying the marked property as the empty
string and its own unrelated property at its declared value.
