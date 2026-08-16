---
audit: config-format-yaml
artifact: decision:config-format-yaml
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:41:50Z
---

# Whether YAML is the single configuration format across the unified file and the per-service configs

Supported. Exactly one YAML decoder site exists in the whole repository, in the shared config loader, and every configuration surface routes through it: the unified file's root loader and its sibling-block re-read, the supervisor config, the two claim-producer services' options loaders, the CLI's own config and context files, the compose manifest and its resolver, the synthetic config the ephemeral run writes, and the license-check tool's table — twelve call sites in all. No competing format is loaded anywhere: searching the tree for the rejected alternatives finds no configuration file in either, and no decoder for either; the JSON files present are build artifacts and tooling settings, not rimsky's configuration surface. The unified file's own concept independently describes it as a single YAML file read by every runtime process, and the comment support the rationale rests on is exercised by the shipped configs, which carry explanatory comments the format admits.
