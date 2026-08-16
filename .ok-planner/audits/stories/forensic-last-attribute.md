---
audit: forensic-last-attribute
artifact: story:forensic-last-attribute
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:45:38Z
---

# A node's most recent resolved attribute bag is read directly rather than reconstructed

Supported. Driven through the public surface against a container of the released
all-in-one image, on a template whose first node cascades to itself and
dispatches three times, so there is a history of bags to be wrong about, and
whose second node reads the first node's attribute. Six checks, none failing.
Both read surfaces asked — the node read and the observability node read —
answered with the same bag, and it was the third dispatch's, not an earlier
one's. The bag carried a resolved input value that no emitted delta ever
contained, and no single event in the log carries the bag, so an operator
working from the log alone would have to fold three deltas together and add the
resolved inputs. The second node's latest bag came back the same way with its
own resolved values.
