---
assessment: sensor-object-store--deposit-becomes-work
subject: story:sensor-object-store
way: deposit-becomes-work
release: d977250c
outcome: held
warrant: experiment:sensor-object-store
---
# Dropping a file into the designated location starts work in the graph

The audit ran `catalog:bundled-services/sensor-object-store` over a location the operator designates with one setting — `catalog:env-vars/RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT` — and nothing else, with the template declaring one publisher of kind `catalog:publisher-kinds/object-store` naming the bucket and prefix. The template's subscription mounted live on the instance. Depositing a file under the designated prefix handed it to the graph as a message describing the object: the backend, the bucket, the object name, its size, its content hash and its modification time. The subscribed node ran on that message, and no operator message was posted at any point in the run, so the deposit alone is what produced the work. Nobody wrote integration code for the producing side; the producer only dropped the file.

## Unverified remainder

One backend and one prefix were exercised. The demonstration does not establish what an object modified in place, rather than newly deposited, produces.
