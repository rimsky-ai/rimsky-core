---
issue: stories-name-rimsky-yml-and-config-keys
kind: audit
category: muddy-boundary
artifacts:
  - story:portable-template-across-modes
  - story:local-orchestrator-zero-config
  - story:claude-agent-mcp-servers-per-node
  - story:claude-agent-expose-env-per-node
status: verified
opened: 2026-07-24T00:00:00Z
---

# User-promise documents are quoting config keys

Rimsky's story documents record durable user promises under a strict rule: a story owns the *need* ("operators can bound which tool servers an agent may reach"), never the *mechanism* (which YAML file, which field name). Two stories about the coding-agent executor's per-node controls still quote exact config keys (`cfg:cli.mcp_servers`, `cfg:cli.expose_env`) and the config file's name in their core statements — mechanism prescription in the one place the form reserves for need. The rule's point is durability: field paths rename freely pre-v1, and every rename silently falsifies whichever story quoted it.

The blocking objection this file previously carried has dissolved: at filing, no decisions existed for the two config keys, so stripping the stories would have left that detail homeless. Three decisions now cover the ground in full (`decision:claude-agent-cli-mcp-servers-inline-only`, `decision:claude-agent-cli-expose-env-field`, `decision:claude-agent-env-passthrough-allowlist`) — the strip has a ready home and is essentially a rewrite-to-cite. What remains is scope judgment, in three layers: the two clear offenders; the softer mentions in other stories ("no config file needed" in the zero-config story — arguably a legitimate user-observable absence, not a mechanism); and how far a catalog-wide sweep goes, which is exactly the territory of two sibling issues (`issue:stories-delivery-surface-named-in-body`, `issue:stories-mechanism-prescription-tail`) covering the same story population for the same class of violation.

## Options

- **Strip the two config-key stories now, sweep the rest as one joint stories pass** with the two sibling issues; absence-mentions read as legitimate observables and stay.
- **Fix only the two clear offenders**, leaving the catalog-wide question to the siblings — same work, weaker coordination.
- **Read absence-mentions as violations too** — maximal purity; strips user-observable facts ("works with zero configuration") that are the story's actual value clause.

The ruling decides scope and the absence-mention reading.

## Ruling

> Generated ruling (/verify-issues): strip the config-file and
> config-key mechanism from the two claude-agent stories, citing the
> three existing decisions that now carry that content, and run the
> wider mentions as part of the single joint stories sweep with
> issue:stories-delivery-surface-named-in-body and
> issue:stories-mechanism-prescription-tail; absence-mentions ("no
> config file needed") are user-observable facts and stay. The
> story-form rule — stories own the need, decisions own the how —
> forces the strip now that the decision homes exist; only the
> editing is sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
