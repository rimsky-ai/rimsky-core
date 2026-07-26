---
issue: endpoint-env-var-collision
kind: human
category: config
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:38:08Z
---

# Three look-alike env vars point at two different things, and 43 more are documented nowhere

Three rimsky binaries each read an environment variable to find an endpoint, and the names practically beg to be confused: the CLI reads `RIMSKY_CONTROL_API`, the peer-enrollment path reads `RIMSKY_CONTROL_API_URL` — both meaning the control API, rimsky's HTTP management surface — and the host-agent (a local daemon on a developer's machine) reads `RIMSKY_URL`, which despite the generic name means the *proxy's* address, a different service entirely. Set the wrong one and nothing errors; things just quietly don't connect. Zooming out makes it worse: live code reads about 58 distinct `RIMSKY_*` variables, and only 15 appear in any project documentation. The other 43 are discoverable only by grepping source.

Two separable questions hide in this: should the colliding names be renamed apart, and should anything stop the *next* env var from shipping undocumented? On precedent: an existing decision (`decision:operator-env-namespaced-per-service`) already namespaces bundled-service vars as `RIMSKY_<SERVICE>_*`, but its scope doesn't cover these core-binary endpoint vars; and the codebase already uses grep-backed "fitness tests" to enforce structural rules elsewhere, so the enforcement mechanism has precedent even though nothing rules it in here. Being pre-v1, renames are cheap now in a way they never will be again.

## Options

- **Fold the CLI's `RIMSKY_CONTROL_API` onto `RIMSKY_CONTROL_API_URL`** so one name means "the control API" everywhere. Breaking rename, ~a dozen call sites.
- **Rename `RIMSKY_URL` to say what it is** (the host-agent-proxy address). Independent of the fold; touches the host-agent config, flag help, and error text.
- **Stand up an enforced env-var registry** — one list every `RIMSKY_*` variable must appear in, backed by a test that fails when a var is read but unlisted. Fixes the documentation rot mechanism, not the collision; composes with the renames or stands alone.
- **Do nothing** — keep the collision risk and the 74%-undocumented gap.

The ruling decides: each rename yes/no (and to what), and registry yes/no (and in what form — hand-written table, generated list).

## Ruling

> Recommended ruling (/recommend-rulings): Rename RIMSKY_URL to
> RIMSKY_HOST_AGENT_PROXY_URL; fold the CLI's RIMSKY_CONTROL_API onto
> the existing RIMSKY_CONTROL_API_URL. Stand up an env-var registry as
> a generated table backed by a fitness test that fails on any
> RIMSKY_* read (including the env-helper call sites) missing from it.
>
> Rationale: Pre-v1 break-freely makes both renames cheap now and
> never again; the proxy var's generic name is the collision's root.
> The fitness test is the mechanical-enforcement ethos applied to the
> 74%-undocumented gap — a registry without a test would rot like the
> docs did.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
