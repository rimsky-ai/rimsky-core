---
issue: go-get-root-module-cannot-resolve
kind: audit
category: conflicting
artifacts:
  - concept:module-layout
status: promoted
opened: 2026-08-16T09:15:28Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The release notes tell consumers to fetch the root Go module at a version that cannot resolve

The root module requires its three sibling modules at placeholder versions satisfied only by workspace replacements, which a consuming module ignores; only the protocols module carries release tags. The release document already records this as a known limitation for installing the CLI by version. Its release-notes template nonetheless tells consumers to fetch the root module at the release version, two sections below the note that says it does not work. The ruling fixes the template; publishing the sibling modules for real is a separate, larger choice the document already declined.

## Options

- Drop the root-module fetch line from the template and keep the protocols-module line, pointing at the existing limitation note; cost: none.
- Publish the sibling modules at real versions and remove the replacements; cost: a release-process change for two more modules, unforced.

The ruling stops the template contradicting the same document.

## Ruling

> Generated ruling (/verify-issues): Remove the root-module fetch instruction from the release-notes template, keep the protocols-module instruction, and point readers at the limitation note the release document already carries. Forced by the document's own already-decided position; publishing the sibling modules remains an open, separate choice. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
