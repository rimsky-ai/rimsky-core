---
experiment: substitution-doc-accuracy
commit: d977250c
---

# The source kinds the product lists, against the ones it resolves

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with the
bundled filesystem claim producer configured over a throwaway workspace, and
drives it through the control API. The only listing of substitution source kinds
this experiment can reach through the public surface is the message the template
validator returns when it rejects an unrecognised source kind. The experiment
reads that list, then drives one template per listed kind through to a dispatch
and reads the resolved value back off the node read surface.

The document the story names — substitution documentation a template author
reads — is not in this repository. The repository carries no documentation
sources and the surface ruling enumerates no documentation kind; the only
prose listing of the source kinds is a package comment on internal source, which
a template author has no public way to read. Re-run unchanged at this tree.

## What was observed

Registering a template whose source directive named `secrets.token` was refused,
and the refusal enumerated six kinds: `claim`, `params`, `nodes`, `messages`,
`child`, `env`.

Each of those six resolved at runtime. A param, an upstream node's attribute, a
typed message's field and a process environment variable each arrived in the
reading node's resolved bag verbatim. A claim resolved in both its payload form
and its claim_scope form. A partition key resolved inside a fan-out work unit.
The set that resolves and the set the refusal lists are the same six.

Nine checks, none failing.
