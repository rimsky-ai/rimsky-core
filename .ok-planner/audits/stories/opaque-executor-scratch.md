---
audit: opaque-executor-scratch
artifact: story:opaque-executor-scratch
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:01:18Z
---

# An executor's opaque bytes survive re-dispatch of the same node-run, uninspected

Supported. Measured with a third-party executor built for the run speaking the
public executor protocol, attaching three distinct byte strings containing
non-UTF-8 bytes and reporting back only the digest and length of whatever a later
dispatch hands it. Ten checks, none failing. All three recovery paths carried the
bytes: a park's resume, an error's retry, and a stale recovery of a dispatch the
runtime reaped for quiet, each a second or third dispatch of the same node-run
id, each stamped with its prior disposition, and each receiving back the exact
length and digest of what its own earlier dispatch had attached — including the
stale-recovery leg reading bytes attached two dispatches earlier. A dispatch with
no predecessor carried none. The non-inspection claim was taken as a count:
across the forty-six rimsky-authored records the run scanned, none carries any of
the three byte strings in base64, hex or raw form, and the park's own audit
record notes only the size.
