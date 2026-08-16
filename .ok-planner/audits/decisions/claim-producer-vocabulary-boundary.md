---
audit: claim-producer-vocabulary-boundary
artifact: decision:claim-producer-vocabulary-boundary
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether the rename covers the shipped surface while the named internal exemptions keep their old names

Supported on both sides of the boundary. Nothing a template author or operator observes carries the old vocabulary: no environment variable, configuration key, template grammar key, command-line verb or flag, or control-API route uses it, the two shipped producer images are named for the new noun, and the only environment variables containing the word belong to the object-store sensor, where it names a kind of external system rather than the producer role. The exemptions are present exactly as described — the fake-producer test helper package, test fixture names, a harness database name, and container-internal paths all keep the old word — as does the per-producer storage layer inside each shipped producer, where the word does its separate storage job. Two remainders sit in the project readme's own prose, both in the ordinary English sense in a section about what the product deliberately is not.
