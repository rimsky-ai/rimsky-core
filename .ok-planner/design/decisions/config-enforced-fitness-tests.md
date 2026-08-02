---
decision: config-enforced-fitness-tests
---

# Config-enforced decisions are proven by grouped fitness tests

## Choice

A decision enforced solely by a configuration surface — the dependency-lint config, the module manifests, the Makefile and image definitions — is linked to code through a grouped fitness test: one Go test file per enforcement surface, asserting the presence and shape of every config-enforced rule it covers and carrying the `@decision:` annotations for all the decisions it proves. Citation tags are never stamped into lint configs, manifests, Makefiles, or Dockerfiles.

## Rationale

The per-edit comment lint polices annotations only in code file types, so a tag stamped into YAML or a manifest would rot unpoliced — a renamed decision would orphan it silently. A grouped fitness test is self-policing (its annotations live in Go, where the lint sees them), fails loudly when the config drops a rule, and gives the periodic implementation audit something exhibitable to point at. The repo already proves config-enforced choices this way — the env-var registry test and the canonicalization-pin check — so this generalizes an existing idiom rather than inventing one.

## Alternatives

- Citation comments inside the config files — rejected: cheapest, but permanently unpoliced by the per-edit lint.
- Exempting config-enforced decisions from annotation — rejected: the audit loses navigation for exactly the decisions hardest to find by reading.
