---
issue: semver-detector-misses-cli-flags-and-root-module
kind: audit
category: test
artifacts:
  - decision:release-semver-from-diff
status: verified
opened: 2026-08-16T10:00:03Z
---

# The release skill's SemVer detector inspects two surfaces where nothing lives

The release skill derives the version bump from the diff's consumer-visible surfaces — protos, migrations, exports, CLI flags, env vars — as its decision commits. Its CLI-flag detector greps the binary entrypoint files for top-level flag-package calls; none of those files references the flag package, and every CLI flag is declared in the CLI library through flag-set variables. Its exported-symbol inspection covers the protocols and foundation modules and omits the root module, which the release notes tell consumers to fetch. A breaking flag or root-module export change produces no finding and is silently classified as a patch. The ruling repoints the two inspections.

## Options

- Repoint the flag detector at the CLI library's flag-set declarations and extend export inspection to the root module; cost: none.
- Restate the decision to name only what the detector reads; cost: drops two consumer surfaces from SemVer with no rationale.

The ruling makes the detector look where the surfaces are.

## Ruling

> Generated ruling (/verify-issues): Repoint the release skill's CLI-flag inspection at the CLI library's flag-set declarations and extend its exported-symbol inspection to the root module alongside protocols and foundation. Forced by the SemVer decision's own commitment to those surfaces; only the inspection locations are wrong. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
