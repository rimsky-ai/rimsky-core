---
audit: validation-author
artifact: story:validation-author
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:04:03Z
---

# A service's own validator is called at registration, its findings blocking or informational

Supported. Measured against a worked example of the story's role — the shipped
shape-checks verifier service, which serves the validation RPC alongside its
primary protocol and advertises the roles it validates for in its capabilities
handshake — run standalone and wired into a released-image stack as a declared
peer. Five legs, nine checks, none failing. The conformance verb for the
validation protocol passed all four of its checks against the service, including
the unsupported-role case. At registration the service's own findings reached the
operator on both sides of the blocking line: an error finding refused the
registration and came back with the service's class and path, while a warning
finding registered the template anyway and came back on the response, and a
template the validator accepts registered with no findings at all. The role
context was the service's own, shown by the path the finding carried. Removing
the mix-in from the peer's declared protocols let the same refused template
register, so the refusal was the service's validator and not a built-in check.
Of the four role contexts the story's parenthetical names, this run exercised the
executor context; the claim-producer and lifecycle-subscriber contexts were not
exercised at this tree.

## Compliance

- The body prescribes the protocol and its mechanism — "implement the validation protocol's single validation RPC and advertise it in my primary protocol's capabilities handshake" names a protocol, an RPC and a handshake, which decisions own; the compliant capability is that the author's own service gets to vet templates before they register.
- The body enumerates the role contexts and the finding kinds — the parenthetical peer-kind list is a surface enumeration; the compliant clause says the validator is called with the context of the role it plays and its findings either block registration or inform.
