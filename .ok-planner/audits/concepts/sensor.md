---
audit: sensor
artifact: concept:sensor
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# Sensors as publisher implementations: subscribe, send, and the emission-failure contract

Supported. Four bundled sensor binaries exist — cron, HTTP poll, object store, and webhook — and every one implements the publisher protocol and nothing else, is registered in the config file's publisher block like any other peer service, and is subscribed by rimsky at instance creation after parameter substitution resolves its config and unsubscribed at instance termination. Each keeps its own per-subscription state in its own embedded store (next fire time, body hash, watermark cursor, last idempotency key) and constructs a message envelope stamped with the subscribed type at fire time, posting it to the universal message-send endpoint with an idempotency key header. The emission-failure contract holds in all four: the shared send helper treats a 4xx other than 408 and 429 as a permanent typed rejection and returns it after one attempt, retries transport errors, 5xx, 408, and 429 within a three-attempt budget, and surfaces exhaustion as an untyped transient error; each sensor then logs its own message-rejected-dropped error line naming the subscription and status and advances its consumed state exactly as on success, while a transient failure returns without advancing anything. The webhook sensor validates its inbound-auth block at subscribe time, requires the block to be present, accepts exactly the three modes the concept names, demands a secret for both authenticating modes and a timestamp header for the signature mode, and answers a transient push failure with a non-success status so the sender retries. Payloads are carried as opaque bytes end to end in all four.

## Compliance

- Self-containment: the emission-failure invariant names the in-repo publisher client package by name; the compliant text says "a typed permanent-rejection error the shared publisher send path returns", naming no package.
- Self-containment: the same invariant quotes the sensors' log-event identifier; the compliant text says the sensor logs the drop at error level naming the subscription and the status, without the identifier.
- Concept altitude: the webhook invariant enumerates the three authentication mode identifiers and the configuration block key; the compliant text states the property — per-subscription inbound authentication is required, and opting out must be typed explicitly — and leaves the mode names to `decision:webhook-auth-required` and to code.
