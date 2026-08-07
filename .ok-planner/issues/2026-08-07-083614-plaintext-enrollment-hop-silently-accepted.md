---
issue: plaintext-enrollment-hop-silently-accepted
kind: audit
category: security
artifacts:
  - concept:peer-auth
  - concept:control-api
  - decision:host-agent-proxy-enrollment
  - decision:enroll-token-is-api-key
status: verified
opened: 2026-08-07T08:36:14Z
---

# A misconfigured enrollment sends the api-key and a private key over an unencrypted connection, silently

When rimsky runs its internal traffic under mutual TLS, every service enrolls
once at startup: it presents its api-key to the platform's control API and gets
back a client certificate to use from then on. That single exchange is the most
sensitive request in the system. It carries the api-key in the clear as a bearer
token on the way out, and on the way back it carries the certificate, the
platform's CA root — **and the certificate's private key**, which the server
generates and returns in the response body
(`lib/control/controlapi/enroll.go::handleEnroll`).

The client that makes that call checks its configuration against four
combinations of two things: whether the control-API address is HTTPS, and
whether a CA root was pinned to verify it against
(`lib/protocols/peerauth/config.go::enrollHTTPClient`). Three are handled
explicitly and refuse to start, each with a message naming the exposure. The
fourth — a plaintext address with no CA pinned — is accepted and returns an
ordinary HTTP client. No error, no warning, no log line. There is no test
covering that arm.

Against a stock deployment this fails closed rather than leaking, because the
control API is not listening in plaintext: its TLS wrapper and its enrollment
route both exist only in mutual-TLS mode. The dial is refused at the transport
and the service exits.

But it exits *after* the request has already gone out. The api-key has left the
process in cleartext by the time anything fails, so the correct remediation
after a misconfigured boot is rotating that key — not just fixing the address —
and nothing in the failure output tells an operator that. Worse, the failure
disappears entirely the moment something else terminates TLS in front of the
control API: a sidecar, an ingress, a service mesh. rimsky cannot distinguish
"plaintext to a TLS terminator on localhost" from "plaintext across a network."
In that topology enrollment succeeds, and the api-key, the certificate, and its
private key all cross the unencrypted leg. One observed enrollment yields a
usable deployment identity for the certificate's full lifetime, and an attacker
positioned on that leg can substitute a CA root of their own — the client has
pinned nothing to compare it against.

The corpus does not govern this arm. Nothing in it says whether a plaintext
enrollment hop is legal under mutual TLS, which is why the code's silence reads
as a decision nobody made rather than one somebody made badly.

## Options

- **Refuse it**: under mutual TLS, require HTTPS. Closes the hole completely;
  forecloses the fronting-terminator topology, which may be a deployment shape
  worth keeping.
- **Require an explicit acknowledgement** — an opt-in setting that says "I know
  this hop is unencrypted." The exposure becomes a recorded operator decision;
  opt-ins get copy-pasted between environments.
- **Warn loudly and continue.** Weakest of the three: a warning in a container
  that then exits is easy to miss, and it leaves the leak fully reachable behind
  a terminator, which is the case that actually leaks.
- **Leave it and document it.** The exposure stays a property nobody opted into.

Two things seem worth doing regardless of which is chosen: the fail-closed error
should say the api-key has already been transmitted, and the arm should get a
test.

The ruling decides whether an unencrypted enrollment address is a configuration
error rimsky refuses, or a posture it permits.

## Ruling

> Recommended ruling (/verify-issues): refuse it. Under mutual TLS, an
> unencrypted enrollment address is a configuration error and rimsky should say
> so at startup rather than transmit an api-key and a private key across it. A
> deployment that genuinely terminates TLS in front of the control API can point
> the service at that terminator's HTTPS address, which is what every other
> client of the control API already has to do — the fronting topology is not
> actually foreclosed, it just stops being reachable by silently dropping to
> plaintext.
>
> Rationale: the other options all preserve the case that leaks, and they do it
> to accommodate a topology that has a working configuration available to it
> already. An opt-in acknowledgement is the strongest alternative and still
> fails the copy-paste test in exactly the environments where it matters; a
> warning is the weakest, because the deployment that leaks is the one where
> everything otherwise appears to work. Separately from the choice: the
> fail-closed message should say the key was already sent, since the remediation
> is rotation and nothing currently tells anyone that, and the arm needs a test
> either way. What would change this call: a deployment shape that genuinely
> cannot present HTTPS at the terminator — that would make refusal a real
> foreclosure rather than a redirection, and the acknowledgement option becomes
> the right one.
>
> Rule this with `issue:no-way-to-export-deployment-ca-root` and
> `issue:proxy-agent-hop-tls-asserted-but-plaintext` — same bootstrap story,
> different ends. Note that refusing plaintext here raises the stakes on the
> first of those: requiring HTTPS makes obtaining the CA root a hard
> prerequisite rather than an awkward one.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
