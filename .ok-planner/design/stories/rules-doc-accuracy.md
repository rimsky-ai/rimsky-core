---
story: rules-doc-accuracy
status: as-is
---

# Contributor trusts rules citations

## Role

As a contributor following the project's after-code-changes verification rules, I can trust that every path and command the rules document instructs me to run actually exists — no missing directories, no stale make target, no non-existent test path — so that acting on the documented verification steps doesn't hit a missing surface.

## Capability

Automated accuracy check over the after-code-changes verification rules: parses every filesystem path and build-target the rules cite, resolves each, and fails if any doesn't exist.

## Business value

Contributors can act on documented verification steps without hitting missing surfaces; documentation rot is caught before it lands.

## Acceptance

An automated accuracy check over the after-code-changes verification rules parses every filesystem path and build-target the rules cite, resolves each, and fails if any doesn't exist. Mutating the rules to cite a non-existent path makes the check fail; the check passes only when every citation resolves to a real artifact.

## Falsifier

The check accepts a non-existent path (text-search only, no resolve), OR the check is informational and doesn't fail CI.

## Proof

Executable proof.
