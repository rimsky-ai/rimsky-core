---
issue: ci-services-shard-builds-unresolvable-image-tags
kind: audit
category: conflicting
artifacts:
  - concept:module-layout
status: verified
opened: 2026-08-16T09:15:25Z
---

# CI's services shard builds images under a tag the test harness will never look for

The services integration harness resolves every rimsky image by a content-addressed source-tree tag (or an explicit override variable) and fails loudly when that image is missing — never falling back to a mutable tag. The CI workflow's services shard builds three images tagged latest, sets no override, and runs the services test target, which builds nothing itself. So the shard either fails on every run at container start or exercises nothing, and its inline comment still documents the retired latest contract. The ruling decides whether CI is a verification path for the container-backed suites.

## Options

- Have the shard export the source-tree tag before building and tagging, so it builds exactly what the harness resolves; cost: the same build minutes it already spends, plus the wiring.
- Drop the image build from the shard, keep the tests that need no images, and record that assembled-stack proof runs only locally via the full target; cost: no CI coverage of the stack-integration path.

The ruling decides whether CI proves the stack.

## Ruling

> Recommended ruling (/verify-issues): Make CI a real verification path — export the derived tag, build the three images under it, and let the harness find them — so the shard proves what it claims to.
>
> Rationale: the workspaces discipline says verification never touches a mutable tag; the shard already pays for the builds, and a green shard that exercises nothing is worse than a red one. Flip case: if CI minutes are the constraint, the second option is honest as long as the release chain (which does run the full target) is the gate that matters.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
