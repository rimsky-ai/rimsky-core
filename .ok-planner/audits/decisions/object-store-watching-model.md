---
audit: object-store-watching-model
artifact: decision:object-store-watching-model
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# One sensor over an object-store abstraction, shipping the filesystem as its only registered backend

Supported. Exactly one sensor serves the deposited-content story, and it is built on a bucket-and-prefix model: a subscription names a backend, a bucket, and a key prefix, and the backend seam is a single-method lister interface taking bucket and prefix and returning object metadata — one operation, so a real object-store backend is the drop-in the rationale describes. Everything the decision says is backend-independent sits above that seam in the shared poll path: subscription handling, the poll interval, both watermark modes, the durable seen-set, idempotent publishing, and the settle window. Two listers exist in the tree. The filesystem one maps a first-level directory under its configured root to a bucket and every regular file beneath it to an object, computing size, digest, and modification time from the file itself. The in-memory one is registered only when an explicit enable variable is set: three tests pin that a stock deployment advertises the filesystem backend alone, refuses a subscription naming the memory backend with an error saying so, and advertises both only under the explicit enable — which is precisely the shipped-store claim. No event-driven detection path exists anywhere in the sensor.
