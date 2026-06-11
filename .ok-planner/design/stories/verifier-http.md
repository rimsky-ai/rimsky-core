---
story: verifier-http
status: as-is
---

# Template author validates via external check service

## Role

As a template author wiring a verifier node against an external check service, I can use the bundled `verifier-http` executor to POST the claim payload to a configured URL and route the node terminal on HTTP status (2xx → success, 4xx/5xx → error with the upstream's class), so that I validate claim outputs against an external service without writing a custom verifier.

## Capability

Bundled `verifier-http` executor: POST claim payload to configured URL; route node terminal on HTTP status; surface upstream's class on 4xx/5xx.

## Business value

Template authors validate claim outputs against an external service without writing a custom verifier; the executor's terminal-on-HTTP-status mapping is faithful.

## Acceptance

A template using `verifier-http` against a real verification service: a payload the service accepts (2xx) reaches a terminal success on the verifier node; a payload it rejects (4xx with a class field) reaches a terminal error with the typed class surfaced; the upstream actually receives the claim payload (echo-back or response-mirror exhibits this).

## Falsifier

The verifier resolves to success when the upstream returned 5xx, OR the upstream's class field is dropped, OR the payload posted is canned.

## Proof

Executable proof.
