---
issue: makefile-go-builds-omit-cgo-disabled
kind: audit
category: test
artifacts:
  - decision:build-cgo-disabled
status: promoted
opened: 2026-08-16T08:48:02Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Four Makefile build targets inherit CGO instead of disabling it

The build decision says CGO is disabled for all builds, so the binaries stay pure Go, cross-compile statically, and need no C toolchain. Every image build and the release CLI build set it. The four Makefile targets a developer actually runs (build, build-all, cli, build-docker) set nothing and inherit the toolchain default, so on a machine with a C toolchain they link against libc. The fitness test that pins the decision reads only three Dockerfiles, so it cannot see the gap. The cli target is the one that produces a distributable-shaped binary. The ruling decides whether dev-loop builds are in the decision's scope.

## Options

- Set CGO off on all four targets and widen the fitness test to every build invocation; cost: a developer wanting CGO for local debugging tools loses it by default.
- Narrow the decision to shipped-artifact builds and say dev-loop targets inherit the toolchain, but bring the cli target in line either way, since it is distributable-shaped; cost: two build postures to keep straight.

The ruling decides whether "all builds" means what it says.

## Ruling

> Recommended ruling (/verify-issues): Make "all builds" true. Set CGO off on the four targets and widen the fitness test's population to every build invocation in the tree.
>
> Rationale: the decision's reason, static and toolchain-free binaries, holds for the developer's own binary too. The cli target ships in shape. One posture is cheaper to keep than a split, and a developer who needs CGO can override on the command line. Flip case: if a debugging or profiling flow genuinely needs a CGO local build, narrow the decision to shipped artifacts and keep cli in scope.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
