---
audit: rimsky-yml
artifact: concept:rimsky-yml
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:15:12Z
---

# The unified config file: eleven invariants, from the single-file claim to strict decoding

Unsupported. Ten of the eleven invariants hold; the single-file claim does not. The producer block is the only surface for declaring claim producers, each declared producer must enumerate its allowed write-semantics or the load fails naming the entry, and the declared set is checked at dial time to be a subset of what the producer advertises, with a mismatch reported against both lists. All persistence connection strings live in the file. Every service entry carries only connection-level fields, entry names stay opaque — a source-parsing test walks the production files in the config package and fails any equality or switch on a service-entry name inside an entry loop — and no key mirrors auth-ledger state. Env-reference expansion is strict: an unset referenced variable aborts the load naming every missing variable rather than substituting empty. Decoding is strict through one shared loader used by the root config, the supervisor block, the CLI, the compose manifests and the bundled services' own option loaders, so an unknown key fails at load with the offending field named and no retired-key shim exists. Late-bind resolution is driven by the per-protocol proxy mapping and an empty mapping leaves the supervisor's resolver purely static. The single-file claim fails: the supervisor role requires a second YAML file of its own, named by a dedicated environment variable and refused as a startup error when unset, carrying process tuning — concurrency, claim-poll interval, and the callback listener's host, port and advertise host. That file is baked into the all-in-one image, written by both compose run paths, and mounted by the integration harness, so it is a live per-process config file rather than a vestige.
