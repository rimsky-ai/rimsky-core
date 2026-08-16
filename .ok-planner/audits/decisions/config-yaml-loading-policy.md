---
audit: config-yaml-loading-policy
artifact: decision:config-yaml-loading-policy
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:41:50Z
---

# Whether every config loader shares one implementation with strict env expansion and strict decoding

Supported on all three parts. The shared implementation is real and singular: the whole repository contains exactly one YAML decoder construction and exactly one call enabling strict field checking, both inside the shared loader package, and twelve loaders across all four modules — the unified file's root loader, its sibling-block re-read, the supervisor config, two service-side options loaders, the CLI, the compose surfaces, and a build tool — call into it rather than decoding themselves. Expansion is bracket-only by construction: the pattern matches only the braced form, so a bare dollar reference is left untouched, and any referenced name absent from the environment collects into a sorted list and returns a hard error naming both the variables and the config path. The divergence routes are closed by three source-walking fitness tests over every non-generated Go file in the repository: one forbids the standard-library expander outside the shared package, one forbids the loose unmarshal helper outside it, and one forbids re-implementing an expansion helper anywhere else. Behavior is pinned too — a root-loader test proves a literal dollar inside a connection string survives while a braced reference beside it expands, both service options loaders assert the unset-variable error names the missing variable, and the retired-alias and retired-template-key tests assert an unknown key is refused at load.
