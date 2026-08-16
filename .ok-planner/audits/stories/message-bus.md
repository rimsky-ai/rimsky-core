---
audit: message-bus
artifact: story:message-bus
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:03:56Z
checked: 4
unaccounted: 0
---

# Sending into an instance's bus, reading it back, and replaying without duplicating

Supported across all four capabilities the story names — send with a mandatory
dedup key, see the history, retrieve one by id, and replay without producing a
duplicate. The dedup key is genuinely mandatory: a send omitting it is refused
outright. Replay was tested in the two ways that can come apart — the same key
with an identical body and the same key with a different body — and both
returned the identity the first send created, with the history holding one row
for that key rather than three, so a replay neither duplicates nor smuggles a
second body in under an accepted key. The history lists both distinct sends
attributed to their sender, fetch-by-id returns the row with its body and
instance, an id never minted is not found, and both bodies reached the
downstream node, which is the "downstream nodes consume the bus" end of the
promise. Thirteen checks, none failing.

## Remediation

- The history capability is obtained through the control-API history route; the CLI tail verb without follow returns only the newest row and drops the older ones, because it de-duplicates against a watermark assuming ascending arrival while the route returns newest-first.
