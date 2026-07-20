---
story: rules-doc-accuracy
status: as-is
---

# Contributor trusts rules citations

## Role

As a contributor following the project's after-code-changes verification rules, I can trust that a path the rules cite in a recognized filesystem-path shape resolves to a real repo artifact, and that a curated set of known-dead references never creeps back in, so that acting on the documented verification steps is unlikely to hit an obviously missing surface.

## Capability

Automated accuracy check over the after-code-changes verification rules: parses backtick-quoted tokens that look like filesystem paths (containing a path separator, and ending in a trailing separator or one of a handful of common file extensions) and resolves each against the repo tree, failing if any is missing. Separately guards a small hardcoded set of known-dead references and one required rebuild instruction. Individual build-target names and path forms outside the recognized shape (a package path ending in an ellipsis, or an extension-less or uncommon-extension path cited without a trailing separator) are not resolved.

## Business value

Contributors can act on the recognized-shape citations without hitting missing surfaces; documentation rot in that recognized shape is caught before it lands.

## Acceptance

An automated accuracy check over the after-code-changes verification rules parses every backtick-quoted, recognized-shape filesystem path the rules cite, resolves each against the repo tree, and fails if any doesn't exist; mutating the rules to cite a non-existent path in that shape makes the check fail. The check separately fails if a hardcoded set of known-dead references reappears, or if a required rebuild instruction is missing. Citations outside the recognized path shape, and individual build-target names, are not resolved or checked for existence.

## Falsifier

The check accepts a non-existent path in its recognized shape (text-search only, no resolve), OR the check is informational and doesn't fail CI, OR the hardcoded dead-reference guard silently stops firing.

## Proof

Executable proof.
