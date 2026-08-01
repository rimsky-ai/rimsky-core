---
issue: src-tag-script-violates-plumbline-lint
kind: human
category: other
artifacts:
  - decision:coding-style
status: verified
opened: 2026-08-01T21:30:00Z
---

# One suite's canonical script fails another suite's lint — the project holds a paper-over

Rimsky consumes two tool suites that both ship files into the repo: ok-workspaces provides `tools/image-src-tag.sh` (the script that computes content-addressed image tags) as a suite-owned file, overwritten wholesale on every suite refresh; ok-plumbline provides the comment-hygiene lint the whole repo must pass. The shipped script fails that lint twice — it carries prose header comments the lint forbids, and a `@decision: content-addressed-src-tag` citation that resolves to no artifact in this project's decision catalog. Because a hand-edit would be silently overwritten at the next refresh, the last session added the script to the lint's ignore list (`file:.ok-plumbline/config.json`) so the repo-wide clean gate passes. That entry is a paper-over holding two suites apart, and nothing in the corpus sanctions it: the coding-style decision commits to the lint running project-wide with no exemption for suite-owned files.

The conflict is upstream by nature — both files are canonical payloads of suites this project doesn't control, and only their common maintainer can make the workspaces payload lint-clean under the plumbline rules (or make the suite's own refresh write the ignore entry). The project-side question is what to hold in the meantime and whether the carve-out becomes policy.

## Options

- Report it upstream to the suites' maintainer, keep the ignore entry as an explicitly temporary bridge, and remove it when a lint-clean payload ships. Cost: resolution waits on an external maintainer.
- Bless the ignore entry permanently as the recorded boundary for suite-owned files. Cost: the lint's project-wide commitment acquires a standing exemption, and the dangling citation ships forever.
- Author a project decision so the script's citation resolves. Cost: adopts suite machinery as project design solely to satisfy a tag the project never wrote.

The ruling decides how the project holds a lint conflict between two suites it doesn't own.

## Ruling

> Recommended ruling (/verify-issues): Report it upstream and treat
> the ignore entry as a temporary bridge — remove it when the
> workspaces payload ships lint-clean.
>
> Rationale: the mismatch is between two suite payloads, so the fix
> belongs to their maintainer; the other options both convert a
> workaround into permanent project posture — one by exempting
> suite files from a lint the corpus commits project-wide, the
> other by inventing a design artifact to satisfy a citation the
> project never authored. Flip case: if upstream declines or the
> suites' contract comes to state that suite-owned files are outside
> project lint jurisdiction, the permanent-exemption option becomes
> the honest answer and should then be recorded, not just configured.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
