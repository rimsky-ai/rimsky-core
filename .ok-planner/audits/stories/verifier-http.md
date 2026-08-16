---
audit: verifier-http
artifact: story:verifier-http
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:04:03Z
---

# The bundled HTTP-callout verifier routes a node's terminal on an external service's answer

Supported. Driven through the public surface against a released-image stack
running the bundled verifier in-process, against a check service that answers
success on one route, a client error naming its own class on another, and a
server error naming a different class on the rest. Three legs, eight checks,
none failing. On the success route the node settled fresh, recorded the status
the service returned, and the service received the exact payload the template
declared, verbatim. On the client-error route the node settled with one failed
run and no fresh run, the terminal class carried the upstream's own class
appended to the verifier's check-failed family, and the payload recorded the
actual status, the expected status and the upstream class. The server-error
route settled failed the same way with that route's class — so both error
families route to error carrying the upstream's class, and nothing in the run
required writing a verifier.
