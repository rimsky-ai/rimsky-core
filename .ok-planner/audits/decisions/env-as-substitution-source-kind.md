---
audit: env-as-substitution-source-kind
artifact: decision:env-as-substitution-source-kind
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:40Z
---

# `env.<VAR_NAME>` is a sixth, fully-integrated substitution source kind

Supported. `lib/graph/attribute/substitution.go`'s `resolveDirectiveValueRaw` dispatch switch carries exactly six source-kind arms — `claim`, `params`, `nodes`/`messages`, `child`, `env` — matching the package doc's six bulleted forms; `resolveEnvValue` reads via `ctx.EnvLookup` (falling back to `os.LookupEnv`), enforces the `[A-Za-z_][A-Za-z0-9_]*` name shape, returns a missing-source error when unset, and returns the empty string when set-but-empty. `lib/graph/attribute/substitution_test.go`'s `TestSubstitute_Env*` cases exercise unset, empty-but-set, the lenient `?` marker, the `| <literal>` fallback, an invalid name shape, and the nil-lookup fallback to the real OS environment — the same lenient/fallback machinery (`resolveDirectiveValue`) that wraps every other kind, confirming uniform treatment. `lib/graph/node/subscription_edges.go`'s `parseSubstitutionDirective`, which is what feeds cascade-edge/coverage derivation, only recognizes `nodes` and `messages` prefixes and returns `false` (no ref extracted) for `env` (and `claim`/`params`/`child`), so an `env` directive induces no subscription edge, exactly as claimed. Registration-time grammar validation in `lib/graph/node/template_validator_directives.go`'s `checkAttributeDirectiveBody` lists `env.` alongside the other five prefixes and validates the same name-shape regex, rejecting out-of-grammar references at registration.
