---
story: verifier-http
status: as-is
---

# Template author validates via external check service

## Story

As a template author wiring a verifier node against an external check service, I can use the bundled HTTP-callout verifier (see `concept:executor`) to POST the claim payload to a configured URL and route the node terminal on HTTP status (2xx → success, 4xx/5xx → error with the upstream's class), so that I validate claim outputs against an external service without writing a custom verifier.

Bundled HTTP-callout verifier (an executor — see `concept:executor`): POST claim payload to configured URL; route node terminal on HTTP status; surface upstream's class on 4xx/5xx.

Template authors validate claim outputs against an external service without writing a custom verifier; the executor's terminal-on-HTTP-status mapping is faithful.
