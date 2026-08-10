---
audit: validation-author
artifact: story:validation-author
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A service's own validator consulted when a template using it is registered

Supported. The shipped shape-checks verifier service is a worked instance of
the story's role — it serves the validation RPC and advertises its supported
role in its own capabilities handshake — and five legs settled the promise
against it. The conformance verb passed all 4 of its checks against the
service. With the service declared as a validation-carrying peer, a template
the service rejects was refused at registration, with the response carrying the
service's finding class and a path showing it was handed the executor role
context; a template the service only warns about registered, with the warning
carried in the same response; and a template the service accepts registered
with no findings. Re-running the refused template against a deployment that
declares the same service without the validation protocol registered it, so the
refusal came from the service's validator and not from rimsky's own checks. Of
the 4 role contexts the story names, 2 were driven — executor here, publisher
by a purpose-built service in the mix-in-uniformity measurement — and
claim-producer and lifecycle-subscriber were not.

## Compliance

The body prescribes wire mechanism: it names the validation protocol, its
single RPC, and the capabilities handshake that advertises it. It also
enumerates the four role names, which is the protocol's vocabulary rather than
the author's need. Compliant text: "As a service author, I want my own
validation rules consulted whenever a template that uses my service is
registered, in the role my service plays for that template, and my findings
surfaced to the operator as blocking errors or informational warnings, so that
I customize validation beyond rimsky's built-in checks."
