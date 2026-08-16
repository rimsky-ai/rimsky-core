---
assessment: sensor-http--egress-allowlist
subject: story:sensor-http
way: egress-allowlist
release: d977250c
outcome: held
warrant: experiment:sensor-http
---
# A private-range poll target stays unreachable until the operator opens it

The audit ran two copies of `catalog:bundled-services/sensor-http`: one whose `catalog:env-vars/RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST` opens the private range the watched location sits in, and one with no allowlist at all. The watch routed to the sensor with no allowlist sent nothing across the whole run, and that sensor's log records the refused dial; the watch routed to the allowlisted sensor polled and sent normally. An operator therefore reaches an internal location only by naming its range, and a watch that names a private target on an unconfigured sensor fails visibly rather than silently succeeding.

## Unverified remainder

One allowlist entry covering one private range was exercised. The demonstration does not establish the sensor's behaviour on a malformed allowlist entry, nor on a target that resolves to a private address only after a redirect.
