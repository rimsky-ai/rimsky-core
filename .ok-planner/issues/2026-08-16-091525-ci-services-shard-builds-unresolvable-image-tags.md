---
issue: ci-services-shard-builds-unresolvable-image-tags
kind: audit
category: conflicting
artifacts:
  - concept:module-layout
status: verified
opened: 2026-08-16T09:15:25Z
---

# CI's services shard builds images under a tag the test harness never looks for

CI's services shard builds images the test harness cannot resolve. The harness resolves every rimsky image by a content-addressed source-tree tag, or by an explicit override variable. It fails loudly when that image is missing, and it never falls back to a mutable tag. The shard builds three images tagged latest, sets no override, and runs the services test target, which builds nothing itself. So the shard either fails at container start on every run or exercises nothing. Its inline comment still documents the retired latest contract. The ruling decides whether CI is a verification path for the container-backed suites.

## Options

- Have the shard export the source-tree tag before it builds and tags, so it builds exactly what the harness resolves; cost: the build minutes the shard spends now, plus the wiring.
- Drop the image build from the shard, keep the tests that need no images, and record that assembled-stack proof runs only locally through the full target; cost: CI covers no part of the stack-integration path.

The ruling decides whether CI proves the stack.

## Ruling

> Recommended ruling (/verify-issues): Make CI a real verification path. The shard exports the derived tag and builds the three images under it. The harness then finds them, and the shard proves what it claims to.
>
> Rationale: the workspaces discipline says verification never touches a mutable tag. The shard pays for the builds now. A green shard that exercises nothing is worse than a red one. Flip case: if CI minutes are the constraint, the second option is honest as long as the release chain is the gate that matters. That chain runs the full target.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
