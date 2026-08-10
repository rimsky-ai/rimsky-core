---
audit: sensor-object-store
artifact: story:sensor-object-store
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Content dropped into a designated location becomes work in the graph

Supported. The operator designated the location with one environment variable on
the bundled object-store sensor and one publisher block naming the bucket and the
prefix; no integration code was written anywhere in the run. Depositing a file
under that prefix produced a message naming the backend, the bucket, the object,
its size, its content hash and its modification time, and the subscribed node ran
on it. A second deposit produced its own message and its own node run. A file
deposited outside the prefix produced no message, while a fourth file under the
prefix did — the later arrival is what shows the sensor kept listing the bucket
across the one it ignored. Over the whole run the graph saw three messages, one
per object under the prefix, none twice, and the subscribed node ran three times.
No operator message was posted at any point.
