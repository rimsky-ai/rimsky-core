---
experiment: assumption-api-key-retrievable-after-mint
commit: d977250c
---

# Can an operator read a key's plaintext back after the mint?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, bootstrapped
with `rimsky auth init`. It mints a key over `POST /v1/auth/keys`, keeps the
plaintext, then reads the key back through both surfaces the prior names —
`GET /v1/auth/keys/{nameOrID}` and `rimsky auth show` — plus the key listing and
three invented reveal query parameters, and finishes by rotating the key.

## What was observed

The plaintext appears once, in the mint response, and nowhere afterwards.
`POST /v1/auth/keys` returns it (201) and the minted key authenticates (200 on
`GET /v1/instances`).

Neither read-back surface returns it. `GET /v1/auth/keys/lost`,
`GET /v1/auth/keys/{id}` and `GET /v1/auth/keys` each answer 200 with six fields
— `id`, `name`, `permissions`, `created_at`, `created_by_key_id`, `last_used_at`
— no field named `plaintext`, and the live plaintext string appears nowhere in
any response body. `rimsky auth show lost` and `rimsky auth list` likewise print
the key without it. Appending `?reveal=true`, `?include_plaintext=true` or
`?show_secret=true` changes nothing: 200, still no plaintext. An invented
`rk_`-prefixed string is refused 401, so the server matches a digest rather than
a name.

Recovery is rotation. `rimsky auth rotate lost --grace 1s` prints a new plaintext
under `Save the new key plaintext now — it will not be shown again`; the new
plaintext differs from the lost one and authenticates immediately, and polling
the old one shows it stop being accepted (401) once the grace passes. The
operator gets a working credential back under the same key name, never the
original secret.

EXPERIMENT PASS (15 checks)
