---
audit: config-format-yaml
artifact: decision:config-format-yaml
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:29Z
---

# YAML is the sole configuration format

Supported. `DecodeStrict` in `lib/protocols/config/loader.go` is the single decode path (confirmed by `TestNoYAMLUnmarshalOutsideSharedLoader`, which fails the build if any `yaml.Unmarshal` call appears outside that package) and is built on `gopkg.in/yaml.v3`; the unified `rimsky.yml`, the CLI's own context config, and every service opts-loader (checked: `lib/services/claim_producers/postgres/server/opts.go`, `lib/services/claim_producers/filesystem/server/opts.go`, and the CLI/compose loaders) route through it. A filesystem search for `*.json`/`*.toml` config files in the project (excluding the unrelated `.ok-workspaces`/`.ok-plumbline` tool configs) and for any other decode call in non-test Go code turned up none, so no second config format exists alongside YAML.
