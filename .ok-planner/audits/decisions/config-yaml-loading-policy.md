---
audit: config-yaml-loading-policy
artifact: decision:config-yaml-loading-policy
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:29Z
---

# One shared strict YAML loader: bracketed-only env expansion, unknown keys hard-fail

Supported. `lib/protocols/config/loader.go`'s `ExpandEnv` matches only `${VAR}` (a bare `$VAR` is left untouched) and returns a load-time error naming every unset referenced variable and the config path; `DecodeStrict` uses `yaml.Decoder` with `KnownFields(true)`. Three lint tests in `lib/protocols/config/lint_test.go` mechanically enforce single-implementation use across the whole repo: `TestNoOSExpandEnvOutsideSharedLoader`, `TestNoYAMLUnmarshalOutsideSharedLoader`, and `TestNoDuplicateEnvExpanderHelpers` walk every non-generated `.go` file and fail on any `os.ExpandEnv`, `yaml.Unmarshal`, or hand-rolled brace-expansion helper outside the shared package. End-to-end behavior is exercised by `lib/control/config/retired_aliases_test.go` (`TestLoadRimskyConfigYAML_RejectsRetiredKeys`, asserting the strict-decode "not found in type"/"unknown" rejection through the root loader `LoadRimskyConfigYAML`), `lib/control/config/persistence_test.go` (bracketed `${...}` DSN expansion), and `test/scenarios/registration_rejects_retired_template_keys_test.go` (unknown-field rejection through the control API's template registration path).
