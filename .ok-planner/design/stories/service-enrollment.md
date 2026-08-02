---
story: service-enrollment
---

# Standing service enrolls and obtains rotating credentials

## Story

As an operator deploying a standing service under mutual-TLS peer auth, I give it a single api-key carrying the enrollment grant; the service obtains its serving credentials at startup and renews them without operator action, and revoking that key stops future issuance — so that I manage exactly one credential per service, mintable, scopeable, and revocable in one place.
