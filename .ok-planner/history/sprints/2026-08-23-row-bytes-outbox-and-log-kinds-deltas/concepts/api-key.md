---
concept: api-key
aliases:
  - bearer token
---

# API key

## What it is

An api-key is a credential rimsky issues and a control-api client presents as a bearer token. The plaintext is a high-entropy string carrying a recognizable prefix. The server keeps only a one-way hash of it in a persisted api-key ledger, and surfaces the plaintext exactly once at mint and once again at each rotation.

## Purpose

An api-key is rimsky's authentication floor: every control-api endpoint can tell who is calling, and an operator mints, rotates, and revokes a key without redeploying. A deployment that needs richer identity terminates that identity at its own edge and injects an api-key downstream. The ledger is rimsky's entire principal registry: rimsky holds no user entity, so a person is the holder of a key's plaintext and a service principal is an api-key (see `concept:service-auth`).

## Boundaries

An api-key owns the plaintext's shape, the one-way hash the server stores, the persisted ledger, and the key's whole life: creation, rotation under a grace period, revocation, and the periodic retirement of a key whose grace has passed. It does not own integration with an external identity system, rate limiting, or the definition of a role. It does not own the certificate machinery that derives a service's short-lived identity from a key carrying the enrollment grant: the api-key is the standing secret, the certificate is the derived identity, and revoking the key stops the certificate's renewal (see `concept:service-auth`). Each key carries a grant, which belongs to `concept:permission`. The deployment state in which no active key exists belongs to `concept:anonymous-mode`.

see also: `permission`, `role-template`, `anonymous-mode`, `event-log`, `service-auth`

## Aliases

- bearer token
