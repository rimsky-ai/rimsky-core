---
issue: peer-and-agent-vocabulary
kind: human
category: design-convention
artifacts:
  - concept:peer-auth
  - concept:service
  - concept:host-agent
  - concept:host-agent-proxy
status: promoted
sprint: 2026-08-23-row-bytes-outbox-and-log-kinds.md
opened: 2026-08-22T05:03:16Z
---

# "Peer" and "agent" confuse the owner; should the vocabulary be renamed?

## Problem

Two terms in the shipped vocabulary do not land with the owner.

**"Peer."** The word entered through the mutual-TLS work (`concept:peer-auth`, `lib/protocols/peerauth`), where it is inherited TLS vocabulary for the other end of a connection. From there it spread: "peer service" for a deployed service on the far side of rimsky's internal protocol boundaries (`concept:service`), "peer protocols" (`concept:conformance`), "dispatch peers" (`concept:service-address-book`), and the proxy's "peer-facing listener". The owner reports the term is hard to follow: it never says *which* other end, and outside the TLS context nothing in the word points at "the deployment's services".

**"Agent" in host-agent.** The host agent (`concept:host-agent`) is an infrastructure daemon on a developer's machine — "agent" in the SSH-agent sense. Rimsky also ships the claude-agent executor, where "agent" means an LLM agent. The owner conflated the two in conversation; the collision is live and will worsen as LLM-agent vocabulary spreads.

One constraint on any rename: a pin test forbids the proxy's source from containing the word "supervisor" — routing must stay blind to who dials in from the deployment side — so the peer-facing listener's replacement name must not name the supervisor.

## Candidates

- Rename "peer" across the corpus and code to a self-describing term ("deployment service", "attached service"), keeping "peer" only inside the TLS mechanism where it is standard vocabulary.
- Rename the host agent to a term without "agent" ("host daemon", "host bridge"), keeping the CLI verb or aliasing it.
- Keep both terms and add the distinctions to the concept docs (`concept:peer-auth` already defines the boundary framing; `concept:host-agent` already says "daemon").
- Any combination: the two terms are independent decisions filed together because both are vocabulary-legibility complaints from the same review.

## Ruling

Both terms go. "Peer" collapses onto the noun the corpus already has, `concept:service`: a peer service is a service, the peer protocols are the service protocols, dispatch peers are the services the address book names, the proxy's peer-facing listener is service-facing, and the mutual-certificate posture is service authentication. The word survives only where it is the TLS library's own vocabulary for the far end of a connection. "Host agent" becomes "host daemon" everywhere: the concept, the binary, the proxy's name and image, the CLI verb, and the environment variables; "agent" stays reserved for the LLM-agent sense the claude-agent executor carries. The proxy's source still never names the supervisor. The owner ruled live on 2026-08-23.
