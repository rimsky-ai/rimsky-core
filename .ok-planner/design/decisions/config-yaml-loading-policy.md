---
decision: config-yaml-loading-policy
---

# Config YAML loading policy

## Choice

Every loader of `rimsky.yml` (root loader, sibling-block re-reads, service-side opts loaders) uses one shared implementation with a strict policy:

- **Env expansion.** References use the `${VAR}` bracketed syntax. A bare `$VAR` is not expanded. Any unset referenced variable is a load-time hard error naming the variable and the config path.
- **YAML decoding.** All loaders use `yaml.Decoder` with `KnownFields(true)`. Any unknown key — typo, guess, stale example, retired key — fails at load with the offending key named.

## Rationale

Silent failure modes (silent-empty on unset env; silent-ignore on unknown keys) let plausible-but-wrong configs run without operators noticing. Both policies make the failure loud and specific. One shared implementation keeps the loaders' semantics from diverging — per-loader copies of the same logic are exactly where divergent expansion and decoding behavior arises.

## Alternatives

- **Opt-in strict knob** — rejected: an escape hatch to lax mode silences the exact error strict mode exists to raise. The project's pre-v1 posture leaves no forward-compat argument for a lax mode.
- **`os.ExpandEnv` (accepts bare `$VAR` too)** — rejected: it substitutes empty strings for unset variables, the silent failure mode the policy exists to eliminate; supporting bare `$VAR` invites shell-substitution confusion in YAML values.
- **Per-loader implementations behind an interface** — rejected: nothing about the three loaders' contexts differs enough to warrant divergent implementations; the abstraction adds indirection without covering any distinct case.
