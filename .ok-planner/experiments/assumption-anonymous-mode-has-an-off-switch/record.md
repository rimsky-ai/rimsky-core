---
experiment: assumption-anonymous-mode-has-an-off-switch
commit: PENDING
---

# Can configuration turn anonymous mode off?

## What it ran against

Seven `rimsky-all-in-one` containers from the tree's own image tag, each on its
own free port. Five boot with a mounted `rimsky.yml` carrying one candidate
anonymous-mode knob; one boots with five candidate environment variables set; one
boots on the zero-config default and is driven through `rimsky auth init`,
`GET /v1/auth/status` and `DELETE /v1/auth/keys/{name}`.

## What was observed

No config key turns anonymous mode off. Each of `anonymous_mode: false`,
`auth: {required: true}`, `require_auth: true`, `anonymous: {enabled: false}` and
`auth_mode: required` stops the container at startup with
`field <name> not found`. The refusal prints the whole top-level schema, and it
holds twelve keys: `persistence`, `claim_producers`, `named_locks`, `executors`,
`publishers`, `validators`, `data_processors`, `retention`, `dispatch_defaults`,
`late_bind_service_proxies`, `peer_auth`, `unreachable_validator_policy`. None is
an auth toggle.

No environment variable turns it off either. A container booting with
`RIMSKY_ANONYMOUS_MODE=off`, `RIMSKY_AUTH_REQUIRED=1`, `RIMSKY_REQUIRE_AUTH=true`,
`RIMSKY_DISABLE_ANONYMOUS=1` and `RIMSKY_AUTH_MODE=required` comes up healthy and
open: with no token, `GET /v1/auth/status` answers 200 with
`{"mode": "anonymous", "active_key_count": 0}`, `GET /v1/auth/whoami` returns the
synthetic identity `{"kind": "anonymous", "key_name": "anonymous"}`, and the
credential-less caller registers a template (201) and mints the deployment's
first api key (201). The startup log carries the WARN banner
`auth.anonymous_mode` — `ANONYMOUS MODE: no API keys provisioned…`.

The only way out is data. On a default container an unauthenticated
`GET /v1/instances` answers 200; after `rimsky auth init` the same request
answers 401 and `auth status` reports `authenticated`. Revoking the last key
puts the deployment back: `DELETE /v1/auth/keys/admin` answers 409 with
`would leave zero active keys (anonymous mode); pass ?force_leave_anonymous=true
to confirm`, and with the flag it answers 200, after which unauthenticated reads
are admitted again.

EXPERIMENT PASS (18 checks)
