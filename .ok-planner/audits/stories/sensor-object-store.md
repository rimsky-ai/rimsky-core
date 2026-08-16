---
audit: sensor-object-store
artifact: story:sensor-object-store
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:35:00Z
---

# Content dropped into a designated location becomes work in the graph, once per object

Supported. Ten checks against a deployment carrying the bundled object-store
sensor over a designated location. The operator designates that location with
one environment setting and nothing else, and the template's subscription
mounted live on the instance. Depositing a file under the designated prefix
handed it to the graph as a message describing the object — the backend, the
bucket, the object name, its size, its content hash and its modification time —
and the subscribed node ran, with no operator message posted at any point in the
run. A second deposit was handed over as its own message and drove its own run.
A file deposited outside the designated prefix was never handed over, while a
later file under the prefix was, which is what shows the sensor kept listing
rather than reading once. Across the run the graph saw three messages for three
objects, none handed over twice, and the subscribed node ran three times.
