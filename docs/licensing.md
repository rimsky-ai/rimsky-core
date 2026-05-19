# Rimsky Licensing — Operator FAQ

Rimsky ships under three licenses applied per-file. The wire surface,
executor SDK, reference store and executor binaries, CLI, conformance
suites, dashboards, and reference deployment artifacts are licensed under
the Apache License 2.0 — embedders can build proprietary executors,
in-house stores, and product-side tooling against them with no copyleft
obligations. The orchestrator-internal layer (scheduler, supervisor,
control-API, persistence, integration runtime, modeling runtime) is
dual-licensed: under AGPL-3.0-or-later by default, or under a separately
negotiated Fall Guy Consulting commercial license. The boundary is
mechanical: `licensing.yml` is the source of truth, and `make license-lint`
verifies it on every CI run. See `COPYRIGHT` for the full per-layer
breakdown and `docs/history/2026-05-02-licensing-design.md` for the
architectural rationale.

## Can I run Rimsky internally without disclosing source?

Yes. AGPL §13 only triggers on offering Rimsky as a network service to
*third parties*. Internal use within a single legal entity — even when
serving thousands of users via internal tools — is unaffected. You may
modify Rimsky and run it on your own infrastructure for your own
operational purposes without publishing those modifications.

## Can I offer Rimsky as a hosted service to my customers under AGPL?

Yes, but you must publish your modifications and let users (downstream
customers) download corresponding source for the orchestrator-layer code.
This is what AGPL §13 requires. The Apache-licensed portions (CLI,
executors, stores, conformance, deploy artifacts) carry no such
obligation.

If you would prefer to operate a hosted service without that publication
obligation, the commercial license track removes it.

## I'm a SaaS company embedding Rimsky inside my product. Do I need a commercial license?

If your product is offered over a network and includes a modified
orchestrator, AGPL §13 reaches it: your users have the right to receive
the corresponding source for the orchestrator portion. The cleanest
answer is the commercial license, which removes that obligation. The
alternative — publishing your modifications to the orchestrator under
AGPL — is also legally valid but typically not what proprietary product
companies prefer.

If you embed only the **unmodified** orchestrator and your modifications
sit in your own executors, stores, or product-side code, those parts are
either Apache-licensed (executors, stores) or untouched by Rimsky's
license. AGPL §13 still requires you to offer the orchestrator's source
to users on request, but unmodified-source-as-shipped already satisfies
that — point them at the upstream repository.

## Can I link my proprietary code to unmodified Rimsky?

If "linking" here means "import the orchestrator-layer Go packages into
my proprietary application and produce a single binary," the FSF reads
that as creating a derivative work, and the combined binary would have
to be AGPL-compatible. The commercial license sidesteps the question.

If "linking" means "run Rimsky as a separate process and talk to it over
its wire protocol," there is no derivative-work relationship: your
process is independent, and the wire protocol is Apache-licensed. This
is the recommended embedding pattern.

## What about my executor / store / CLI extensions?

Those are Apache-licensed. The wire IDL, the executor SDK packages
(`@fallguy/claude-agent`, `executors/http-node/`, `executors/stub/`),
the reference store binaries (`stores/filesystem/`, `stores/postgres/`,
`stores/stub/`), and `rimsky` are all Apache 2.0. Build whatever you
want; you owe nothing back. (Contributions back are welcomed under the
CLA in `CLA.md`, but are not required.)

## Can I write a closed-source executor that talks to Rimsky?

Yes. Executors are independent processes that speak the wire protocol;
the protocol is Apache-licensed. There is no license relationship between
your executor and the Rimsky orchestrator beyond the wire. The same
applies to closed-source store implementations and closed-source CLIs.

## Can I fork Rimsky and offer hosted Rimsky-as-a-Service?

Under AGPL, yes — provided you publish your modifications under AGPL and
make the corresponding source available to your users. We discourage it
(this is what the AGPL is designed to deter, and what the commercial
license is designed to capture) but do not forbid it.

The trademark policy in `TRADEMARKS.md` does prohibit calling the fork
"Rimsky" or "Rimsky-<anything>". Choose a distinct project name. The
"Rimsky-compatible" label is available if your fork passes the upstream
conformance suite.

---

For commercial license inquiries, contact licensing@fallguyconsulting.com.
For the architectural rationale, see
`docs/history/2026-05-02-licensing-design.md`.
