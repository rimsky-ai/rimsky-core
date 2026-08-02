---
audit: run-name
artifact: decision:run-name
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# Run name defaults from the manifest's project field

Supported. `rimsky compose run`'s run-name resolution (tagged `@decision: run-name`) defaults `name` to the manifest's `Project` field when the `--name` flag is empty, then passes it straight to `EnsureRunDir`, which joins it directly into the run-directory path. The manifest's `Project` field is validated at manifest-parse time against `projectRe` (the same identifier grammar used elsewhere for filesystem-safe names) and is required, matching "already filesystem-safe by construction." The `--name` override flows into the same `EnsureRunDir` call with no equivalent regex check applied anywhere in the compose command package, matching the decision's claim that the override is unvalidated relative to the manifest's grammar.
