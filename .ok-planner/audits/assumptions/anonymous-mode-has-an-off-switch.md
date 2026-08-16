---
assumption: anonymous-mode-has-an-off-switch
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# anonymous mode can be turned off by configuration, so an operator can guarantee a fresh deployment never admits an unauthenticated request.

As operator hardening a deployment, I would take it that anonymous mode can be turned off by configuration, so an operator can guarantee a fresh deployment never admits an unauthenticated request.

## Source

ecosystem-prior — bootstrap-open modes in control planes normally carrying a disable flag

## What a run would observe

search the config keys and env vars for an anonymous-mode toggle and try booting the control API with authentication forced on and no keys.

## Measured

Experiment `assumption-anonymous-mode-has-an-off-switch`, re-run at this tree
across seven `rimsky-all-in-one` containers. No configuration turns anonymous
mode off. Each of `anonymous_mode: false`, `auth: {required: true}`,
`require_auth: true`, `anonymous: {enabled: false}` and `auth_mode: required`
stops the container at startup with `field <name> not found`, and the refusal
prints the whole top-level schema — twelve keys, `persistence`,
`claim_producers`, `named_locks`, `executors`, `publishers`, `validators`,
`data_processors`, `retention`, `dispatch_defaults`, `late_bind_service_proxies`,
`peer_auth`, `unreachable_validator_policy` — none of them an auth toggle. A
container booting with `RIMSKY_ANONYMOUS_MODE=off`, `RIMSKY_AUTH_REQUIRED=1`,
`RIMSKY_REQUIRE_AUTH=true`, `RIMSKY_DISABLE_ANONYMOUS=1` and
`RIMSKY_AUTH_MODE=required` comes up healthy and wide open: with no token,
`GET /v1/auth/status` reports `{"mode": "anonymous"}`, `GET /v1/auth/whoami`
returns `{"kind": "anonymous", "key_name": "anonymous"}`, and the credential-less
caller registers a template and mints the deployment's first api key. The
operator hardening a deployment cannot guarantee a fresh one never admits an
unauthenticated request: the mode is data-derived, and the window stays open
until a key exists. What the product gives instead is a loud WARN banner
(`auth.anonymous_mode`: `ANONYMOUS MODE: no API keys provisioned…`), an
immediate flip the moment `rimsky auth init` mints the first key (the same
unauthenticated read turns 401), and a guard on the way back — revoking the last
key answers 409 unless `?force_leave_anonymous=true` accompanies it.
