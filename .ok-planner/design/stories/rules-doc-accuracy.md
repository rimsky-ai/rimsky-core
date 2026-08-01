---
story: rules-doc-accuracy
status: as-is
---

# Contributor trusts rules citations

## Story

As a contributor following the project's after-code-changes verification rules, I can trust that a path the rules cite in a recognized filesystem-path shape resolves to a real repo artifact, and that a curated set of known-dead references never creeps back in, so that acting on the documented verification steps is unlikely to hit an obviously missing surface.

Automated accuracy check over the after-code-changes verification rules: parses backtick-quoted tokens that look like filesystem paths (containing a path separator, and ending in a trailing separator or one of a handful of common file extensions) and resolves each against the repo tree, failing if any is missing. Separately guards a small hardcoded set of known-dead references and one required rebuild instruction. Individual build-target names and path forms outside the recognized shape (a package path ending in an ellipsis, or an extension-less or uncommon-extension path cited without a trailing separator) are not resolved.

Contributors can act on the recognized-shape citations without hitting missing surfaces; documentation rot in that recognized shape is caught before it lands.
