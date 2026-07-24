---
decision: config-yaml-loading-policy
status: as-is
---

# Config YAML loading policy

## Choice

Every loader of `rimsky.yml` (root loader, sibling-block re-reads, service-side opts loaders) uses one shared implementation with a strict policy:

- **Env expansion.** References use the `${VAR}` bracketed syntax. A bare `$VAR` is not expanded. Any unset referenced variable is a load-time hard error naming the variable and the config path.
- **YAML decoding.** All loaders use `yaml.Decoder` with `KnownFields(true)`. Any unknown key — typo, guess, stale example, retired key — fails at load with the offending key named.

## Rationale

Silent failure modes (silent-empty on unset env; silent-ignore on unknown keys) let plausible-but-wrong configs run without operators noticing. Both policies make the failure loud and specific. One shared implementation prevents the byte-identical-duplication + divergent-semantic pattern that this decision replaces.

## Alternatives

- **Opt-in strict knob** — rejected: an escape hatch to lax mode silences the exact error strict mode exists to raise. The project's pre-v1 posture leaves no forward-compat argument for a lax mode.
- **`os.ExpandEnv` (accepts bare `$VAR` too)** — rejected: silent-empty on unset was one of the divergent semantics this decision unifies; supporting bare `$VAR` invites shell-substitution confusion in YAML values.
- **Per-loader implementations behind an interface** — rejected: nothing about the three loaders' contexts differs enough to warrant divergent implementations; the abstraction adds indirection without covering any distinct case.

## Proof

Executable proof — a lint asserts zero occurrences of `os.ExpandEnv` or bare `yaml.Unmarshal` calls outside the single shared loader package that carries this decision's `@decision:config-yaml-loading-policy` annotation. A new bare `yaml.Unmarshal` call, a new `os.ExpandEnv` on config-derived text, or a duplicate env-expansion helper elsewhere in the tree all fail the lint.
