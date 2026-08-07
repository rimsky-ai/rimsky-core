---
issue: empty-struct-doc-comments-in-projected-references
kind: audit
category: doc-drift
artifacts: []
status: promoted
opened: 2026-08-06T06:49:21Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# Undocumented public structs project as blank cells in generated references

The docs pipeline generates reference tables for the public wire and
template structs from their Go doc comments. Three load-bearing
structs — the template spec, the claim-handle state, and the
terminal-error payload (`lib/foundation/spec/template.go`,
`spec/claim_handle_state.go`, `signal/payloads.go`) — carry no doc
comments, so every generated reference shows empty description cells
for them and their fields, including semantically subtle pairs like
attempt counters whose relationship nothing states.

The obvious fix — write one-line doc comments — is not actually
available to a working session: this codebase forbids comments except
in files carrying an explicit docstring opt-in marker, only two files
in the repo have it, and adding the marker is reserved for the
owner's call about what counts as a documented public-API surface.
That prohibition is not an obstacle so much as the project's stated
position: prose truth belongs in the design corpus, and this session
has already resolved the analogous proto-comment issues by deleting
the prose and pointing the docs projection at the corpus instead.

## Options

- Opt the three files into docstrings and write the comments. Cost:
  reverses the direction just taken for the protos — it re-blesses
  code comments as a load-bearing docs source.
- Keep code comment-free and fill the references from the corpus
  (field semantics live in the concept catalog; the docs generator
  learns to draw descriptions from there). Cost: the generator change
  is docs-repo work this repo can only motivate, not make.
- Encode the subtle semantics in code — names, assertions, tests —
  and accept blank description cells. Cost: reference tables stay
  visibly incomplete.

The ruling decides where struct-field descriptions come from.

## Ruling

> Recommended ruling (/verify-issues): keep the code comment-free and
> make the design corpus the source the references draw from — the
> same direction just ruled for the proto comments. Where a field's
> semantics are subtle enough to need stating (the attempt-counter
> relationship), state them in the owning concept; the docs
> generator's switch from godoc to corpus is the docs repo's work
> item, motivated from here.
>
> Rationale: opting files into docstrings would rebuild, in Go, the
> exact comments-as-published-truth surface this project just tore
> out of the protos; one direction should govern both. The flip
> case: if the owner decides these three files are a deliberate
> documented-API surface — the thing the opt-in marker exists for —
> then docstrings are the sanctioned tool and this ruling inverts.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
