---
story: peer-tls-enforced
status: as-is
---

# Operator enforces TLS on peer connections

## Story

As an operator who configures TLS as required on a peer service (executor or store), I get a TLS-verified connection to that peer — and a loud failure if the peer cannot present credentials — so that the TLS config key means what it says.

The TLS config key is writable on executor, store, and publisher peer entries and is a validated two-value enum (off or required; empty defaults to off). Every peer dial site honors the configured mode: required dials with verified TLS against system roots; off stays plaintext; failures under required name the peer and the mode (see `decision:peer-tls-enforcement`, `decision:tls-mode-validation`).

A security-shaped config key that is accepted and ignored manufactures false confidence exactly where it is costliest; with enforcement at every dial site, the key means what it says for every peer kind.
