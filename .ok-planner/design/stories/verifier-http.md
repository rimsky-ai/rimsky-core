---
story: verifier-http
---

# Template author validates via external check service

## Story

As a template author wiring a verifier node against an external check service, I can use the bundled HTTP-callout verifier (see `concept:executor`) to POST the claim payload to a configured URL and route the node terminal on HTTP status (2xx → success, 4xx/5xx → error with the upstream's class), so that I validate claim outputs against an external service without writing a custom verifier.
