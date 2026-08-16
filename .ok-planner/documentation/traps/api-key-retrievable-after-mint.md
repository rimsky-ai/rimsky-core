---
trap: api-key-retrievable-after-mint
release: d977250c
demonstration: experiment:assumption-api-key-retrievable-after-mint
---
## Assumption

As operator who lost a key, I would take it that `rimsky auth show` or `GET /v1/auth/keys/{nameOrID}` can return the key material again, since both a show verb and a get route exist.

name-promise — `rimsky auth show` and `GET /v1/auth/keys/{nameOrID}` alongside `rimsky auth create-key`

## Actual behavior

Experiment `assumption-api-key-retrievable-after-mint`, re-run at this tree
against one `rimsky-all-in-one` container. Neither surface returns the key
material again. `POST /v1/auth/keys` surfaces the plaintext once, in its own
response; afterwards `GET /v1/auth/keys/{name}`, `GET /v1/auth/keys/{id}` and
`GET /v1/auth/keys` each answer 200 with six fields — `id`, `name`,
`permissions`, `created_at`, `created_by_key_id`, `last_used_at` — no field named
`plaintext`, and the live plaintext appears nowhere in any body. `rimsky auth
show` and `rimsky auth list` print the same key without it, and `?reveal=true`,
`?include_plaintext=true` and `?show_secret=true` change nothing. An invented
`rk_`-prefixed string is refused 401, so the server matches a stored digest. The
operator who lost a key recovers by rotating it: `rimsky auth rotate lost
--grace 1s` prints a new plaintext under `Save the new key plaintext now — it
will not be shown again`, the new plaintext authenticates at once, and the lost
one stops being accepted when the grace passes. The name and the grant survive;
the original secret does not.
