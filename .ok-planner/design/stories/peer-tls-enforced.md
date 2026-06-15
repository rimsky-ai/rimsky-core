---
story: peer-tls-enforced
status: as-is
---

# Operator enforces TLS on peer connections

## Role

As an operator who configures TLS as required on a peer service (executor or store), I get a TLS-verified connection to that peer — and a loud failure if the peer cannot present credentials — so that the TLS config key means what it says.

## Capability

The TLS config key is writable on executor, store, and publisher peer entries and is a validated two-value enum (off or required; empty defaults to off). Every peer dial site honors the configured mode: required dials with verified TLS against system roots; off stays plaintext; failures under required name the peer and the mode (see `decision:peer-tls-enforcement`, `decision:tls-mode-validation`).

## Business value

A security-shaped config key that is accepted and ignored manufactures false confidence exactly where it is costliest; with enforcement at every dial site, the key means what it says for every peer kind.

## Acceptance

With TLS set to required on a peer entry, rimsky dials that peer with verified TLS; against a TLS-serving peer the connection works end-to-end; against a plaintext peer the dial fails with an error naming the peer and the mode. With TLS set to off (and by default) behavior is plaintext.

## Falsifier

A peer connection configured with TLS required observed on the wire in plaintext; or the TLS config key accepted and silently ignored.

## Proof

Executable proof — integration test dials a TLS-enabled stub peer under the required mode and exchanges a request; companion test dials a plaintext stub under the required mode and asserts the loud failure.
