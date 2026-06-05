# Claude-agent sign-off gate — Divergence record

Audit of the working tree against `.ok-planner/plans/2026-06-04-claude-agent-signoff-gate.md`
(and its spec). Build is clean and the full sign-off suite passes (32 tests). The
implementation tracks the plan closely; the items below are the meaningful
deltas — extractions/elaborations the plan left unspecified, and a few
test-fidelity gaps versus what the plan literally called for. None is a
correctness defect.

---

## 1. `parseCliConfig` parsing was extracted into three named helpers, not inlined

- **What the plan said** (Task 4, steps 1–2): "extend the return type and parsing
  to add … parsed from `cli.mcp_servers` (validate each entry has string `name` +
  `url`; pass `headers`/`allowed_tools` through if present) … Keep the
  type-shape-only validation style of the existing parser." The plan describes
  inline parsing inside `parseCliConfig`.
- **What was implemented:** the parsing was factored into three new module-private
  helpers in both copies — `parseMcpServers`, `parseRequiredSignoffs`, and
  `parseStringRecord` (`src/server.ts:738-816`, mirrored at
  `src/http-bridge.ts:498-565`). `parseStringRecord` is brand-new (it did not exist
  before this change). The new helpers are annotated `@source: src/server.ts
  (…)` to track the duplication into the http-bridge mirror.
- **Inferred reason:** the per-entry validation is non-trivial loop logic; pulling
  it out keeps `parseCliConfig` readable and lets the `@source:` tracked-duplication
  discipline name each mirrored helper. Consistent with the cold-read cheatsheet.

## 2. Host-server `headers` are dropped when empty (`parseStringRecord` returns `undefined` for `{}`)

- **What the plan said** (Task 4, step 1): "pass `headers`/`allowed_tools` through
  if present."
- **What was implemented:** `parseStringRecord` (`src/server.ts:811-815`) returns
  `undefined` for an empty or all-non-string map, so a present-but-empty
  `headers: {}` becomes "no headers" rather than an empty object. Same drop-empties
  behavior for `allowed_tools` via the pre-existing `stringArrayOrUndefined`.
- **Inferred reason:** matches the surrounding parser's prevailing "empty ⇒
  undefined" idiom (every other field collapses empties). Behaviorally harmless —
  `mcpConfigJson` already defaults absent headers to `{}` at serialization
  (`src/cli-runner.ts:322`).

## 3. `maxSignoffAttempts` accepts any finite number (including negatives → treated as default 3)

- **What the plan said** (Task 4, step 1): "`maxSignoffAttempts?: number` from
  `cli.max_signoff_attempts` (reuse the existing `numberOrUndefined`)."
- **What was implemented:** parsing does reuse `numberOrUndefined` (which only
  checks `Number.isFinite`), but the *gate* then applies a `>= 0` guard
  (`src/agent-run.ts:580-583`): a negative or non-number `maxSignoffAttempts`
  silently falls back to the default of 3 rather than being rejected at parse time.
  A negative value is therefore parsed-through but never honored.
- **Inferred reason:** the plan's own Task 10 snippet contained this exact `>= 0`
  guard, so the implementer followed the snippet. The schema adds `minimum: 0`
  (`src/expected-attributes-schema.ts`), so rimsky-side validation would reject a
  negative before dispatch anyway; the in-gate fallback is defense-in-depth, not a
  contradiction. Recorded because the parse layer and the gate layer disagree on
  what a negative means (parse: keep it; gate: ignore it).

## 4. `CliResumeRequest.allowedTools` was added (the plan flagged this as conditional)

- **What the plan said** (Task 6, step 3): "check `CliResumeRequest` in
  `cli-runner.ts` — **if it lacks an allow-list field**, add `allowedTools` to it
  and emit it … mirroring the spawn path." Conditional.
- **What was implemented:** the field was indeed absent, so it was added
  (`src/cli-runner.ts:128-138`) and `buildClaudeCliResumeArgs` now passes
  `req.allowedTools` into `buildAllowedTools` (`src/cli-runner.ts:300`). Both resume
  spawn sites in `agent-run.ts` (post-park ~`:840`, exit-recovery ~`:1076`) now emit
  `allowedTools: allowedToolsUnion`.
- **Inferred reason:** the conditional resolved to "add it" because the field was
  missing; this is the plan branch firing as designed, recorded only because the
  plan left it contingent on inspection.

## 5. The `allowedTools` union is hoisted into one shared `allowedToolsUnion` const, used at all three sites

- **What the plan said** (Task 6, step 2): "change `allowedTools:
  cliConfig?.allowedTools` to a union: `allowedTools: [...(cliConfig?.allowedTools
  ?? []), ...hostAllowed]` (pass `undefined` when both are empty …)." The plan
  shows the union computed inline at the spawn site.
- **What was implemented:** the union is computed once as `templateAllowed` /
  `hostAllowed` / `allowedToolsUnion` before the spawn block
  (`src/agent-run.ts:809-818`) and referenced at all three sites
  (`:842`, `:871`, `:1080`). Same value, single derivation.
- **Inferred reason:** the plan requires the same union at three call sites;
  computing it once avoids triplicated inline expressions and a drift risk. A
  cleaner shape than the literal inline form, with identical behavior.

## 6. Plan's "(or the `mcp__validator__*` entries)" allowlist ambiguity resolved to the bare server-prefix form

- **What the plan said** (Task 5, step 1): assert `req.allowedTools` "contains
  `mcp__validator` (or the `mcp__validator__*` entries)" — left open which form a
  no-`allowed_tools` server produces.
- **What was implemented:** a server with no explicit `allowedTools` emits the bare
  `mcp__validator` server-prefix entry (`src/agent-run.ts:803-806`); the wiring test
  asserts exactly `mcp__validator` and a second test asserts the narrowed
  `mcp__validator__sign` / `__info` form and that the broad prefix is then absent
  (`src/mcp-servers-wiring.test.ts:122,162-165`).
- **Inferred reason:** the plan's step-1 prose for `hostAllowed` already prescribed
  this exact behavior ("a bare `mcp__<name>` server-prefix entry allows all"); the
  implementer picked the matching assertion. Recorded because the plan's *test*
  text offered two acceptable forms and one was chosen.

## 7. The wiring test (Task 5) lives in `mcp-servers-wiring.test.ts` but its Pass-3 verification command was the config test

- **What the plan said:** Pass 3 verification (line 196) is `npx vitest run
  src/mcp-servers-wiring.test.ts && npm run build`, and Task 5 creates that file.
  Pass 2 verification (line 158) is `npx vitest run src/config-signoff.test.ts`.
- **What was implemented:** both files exist as named
  (`src/mcp-servers-wiring.test.ts`, `src/config-signoff.test.ts`) and both pass.
  No divergence in file placement.
- **Inferred reason:** n/a — listed only to confirm the pass-to-file mapping landed
  as written. **Not a divergence.**

## 8. e2e acceptance test uses a fresh bridge per case and asserts on a shared `posts` buffer rather than a runId discriminator

- **What the plan said** (Task 9): one e2e test with an unsigned case and a signed
  case, asserting the captured `AsyncCallbackBody`. The plan did not specify whether
  the two cases share a bridge or how callbacks are disambiguated.
- **What was implemented:** the test shuts down the unsigned bridge and starts a
  second bridge for the signed case, resetting `posts.length = 0` between them
  (`src/signoff-gate.e2e.test.ts:211-237`); both cases reuse the same `DISPATCH_ID`
  ("acc-disp-1") and assert `posts[0]`. Because the bridge is torn down between
  cases, `posts[0]` is unambiguous.
- **Inferred reason:** reusing one `dispatch_id` across both runs is only safe
  because the bridges are separate processes with separate callback capture; the
  implementer chose bridge-per-case isolation over runId-keyed assertions. Sound,
  but a structural choice the plan left open.

## 9. e2e signed/unsigned cases assert presence/absence of `success`/`error` keys, not the strict "exactly one outcome key" envelope

- **What the plan said** (Task 9): assert the body is `{ error: { error_class:
  "agent/signoff_unobtained", … } }` (unsigned) and `{ success: { … } }` (signed).
  CLAUDE.md notes the `AsyncCallbackBody` invariant is "exactly one of the
  success/error/park outcome keys."
- **What was implemented:** the test asserts `unsignedBody.success` is `undefined`
  and `error.error_class === "agent/signoff_unobtained"` (`:206-209`), and
  `signedBody.error` is `undefined` and `success.attributes_delta` equals the delta
  (`:261-264`). It does not assert that `park` is absent or that *exactly* one key
  is present.
- **Inferred reason:** the relevant load-bearing observable (which outcome key, and
  its class/payload) is covered; the broader "exactly one key" property is the
  supervisor's invariant, exercised elsewhere. A reasonable scoping of the
  acceptance assertion, narrower than a literal reading of the envelope contract.

## 10. `canonicalize` import required a CJS/ESM interop shim the plan did not anticipate

- **What the plan said** (Task 1, step 2): `import canonicalize from
  "canonicalize";` — a plain default import.
- **What was implemented:** `src/signoff.ts:6-15` imports
  `* as canonicalizeModule` and normalizes `canonicalizeModule.default ??
  canonicalizeModule` behind a `Canonicalize` type, because under NodeNext the
  package's `.d.ts` declares an ESM default while its runtime export is a bare CJS
  function — a plain default import is non-callable at type-check time.
- **Inferred reason:** real packaging mismatch in `canonicalize@2.1.0` surfaced only
  at build; the implementer added the minimal interop shim to keep `npm run build`
  clean. A necessary deviation from the plan's import snippet, behaviorally
  identical.

## 11. `signoffs` parameter ordering in `onComplete` matches the plan's stated preference (recorded for completeness)

- **What the plan said** (Task 7, step 1): place `signoffs` before
  `scheduleTeardown` to keep `scheduleTeardown` last.
- **What was implemented:** exactly that — `onComplete(attributesDelta, changed,
  changeSummary, signoffs, scheduleTeardown)` in `src/token-registry.ts:46-58`, with
  the live handler forwarding `args.signoffs ?? null`
  (`src/internal-mcp-server.ts:358`) and the registered callback consuming it
  (`src/agent-run.ts:643`). **Not a divergence** — confirmation that the optional
  ordering call landed on the plan's recommended side.
