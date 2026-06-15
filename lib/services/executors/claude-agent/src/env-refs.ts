// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// @deliberate: environment-reference resolution for spawn-boundary secrets.
// S-executors-validator-header-secret-refs.
//
// A node may wire an auth-gated host MCP server (a validator, a tool server)
// whose connection headers carry a credential. Persisting that credential in
// plaintext node attributes — where the supervisor traces/stores them — would
// leak the secret. Instead the operator references it as `${env:VAR}` in the
// header value; the executor resolves the reference against its OWN process
// environment ONLY at spawn time, when assembling the transient `--mcp-config`
// the CLI reads. The parsed `cli.mcp_servers` form the supervisor persists
// keeps the unresolved `${env:VAR}` reference, so the resolved secret exists
// only in the per-dispatch spawn surface and never lands in persisted state.

/**
 * Replace every `${env:VAR}` token in `value` with the value of the named
 * environment variable, or the empty string when the variable is unset.
 *
 * `VAR` matches POSIX-ish env-var name characters (`[A-Za-z_][A-Za-z0-9_]*`).
 * A token whose variable is unset resolves to `""` rather than being left
 * verbatim — leaving an unresolved `${env:...}` on the wire would ship a
 * broken credential to the server, which fails loud (e.g. a 401) anyway; the
 * empty resolution makes "secret not provisioned" the operator-visible failure
 * instead of a confusing literal-reference bearer.
 *
 * Resolution is purely textual: the surrounding literal characters (e.g. the
 * `Bearer ` prefix) are preserved, so `"Bearer ${env:VALIDATOR_TOKEN}"`
 * becomes `"Bearer <token>"`.
 */
export function resolveEnvRefs(value: string): string {
  return value.replace(/\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}/g, (_match, name) => {
    return process.env[name] ?? "";
  });
}

/**
 * Apply {@link resolveEnvRefs} to every value of a header map, returning a NEW
 * map (the input is not mutated, so the caller's persisted/traced reference
 * form is untouched). `undefined` in → `undefined` out.
 */
export function resolveHeaderEnvRefs(
  headers: Record<string, string> | undefined,
): Record<string, string> | undefined {
  if (headers === undefined) return undefined;
  const resolved: Record<string, string> = {};
  for (const [key, val] of Object.entries(headers)) {
    resolved[key] = resolveEnvRefs(val);
  }
  return resolved;
}
