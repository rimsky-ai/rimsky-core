---
audit: cascade-flags-required-no-defaults
artifact: decision:cascade-flags-required-no-defaults
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# Cascade-shape flag is required on every subscription entry

Supported. `lib/graph/node/template_validator.go::validateSubscribes` checks `s.ForceUpstreamRefresh != nil` for every subscription entry and appends a registration-rejecting error naming the `.force_upstream_refresh` path with a "required ... no default applies" message when absent; `SubscriptionEntry.ForceUpstreamRefresh` is typed as `*bool` (a pointer, so Go's zero value cannot silently satisfy it) rather than `bool`. `lib/graph/node/template_validator_subscribes_test.go::TestValidateSubscribes_RejectsMissingForceUpstreamRefresh` checks this directly: a template with a subscription entry omitting the field fails validation with an error path ending `.force_upstream_refresh` containing "required".
