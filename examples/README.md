# rimsky protocol examples

Minimal, **copy-and-modify** servers — one per rimsky protocol a consumer
implements. Each is the smallest thing that registers, speaks its protocol
correctly, and is exercised by a test. They exist to carry the Go wiring that
prose can't: the generated import path, the streaming-server method signatures,
how to build the protobuf `oneof` terminals, and the startup-handshake answers
the supervisor requires.

These are **not** test doubles (rimsky's own test stub lives at
`test/support/executors/stub/`) and **not** deployable services. Copy a
directory, rename the module, and replace the body with your work.

## License

This `examples/` module is **Apache-2.0**, like `lib/protocols/` (the wire
contract) — so you may copy it into your own project freely. The rest of
rimsky-core is AGPL; these examples deliberately depend only on the Apache
`lib/protocols` module and stdlib + permissive third-party packages.

## Layout

| Directory | Protocol | Guarantee |
|---|---|---|
| `executor/` | `Executor` (+ `ExecutorObservability` handshake) | in-process gRPC behavioral test |
| `claimproducer/` | `ClaimProducer` (read-only) | in-process gRPC behavioral test |
| `atomic-staging-fs-producer/` | `ClaimProducer` (staged-write: stage-at-Open, atomic-rename-on-Commit, drop-on-Abandon over a POSIX filesystem) | in-process gRPC behavioral test + sweep unit test |
| `lifecyclesubscriber/` | `LifecycleSubscriber` | behavioral test |
| `publisher/` | `Publisher` (in-memory subscriptions) | in-process gRPC behavioral test + cross-stack proof against a running rimsky (`main_e2e_test.go`; see `publisher/README.md`) |
| `validation/` | `Validation` (registration-time mix-in; routes the `ValidateRequest` role oneof, executor arm) | in-process gRPC behavioral test |
| `data-processing/` | `DataProcessing` (fan-out candidate lifecycle: BeginCandidate → CommitCandidate / AbandonCandidate) | in-process gRPC behavioral test |
| `compose/` | `rimsky compose` manifest (not a protocol server) | manifest loads + each template validates |

To run a copied example against the real conformance harness once you've filled
in your logic, see `rimsky conformance executor` (and the Go libraries under
`lib/protocols/conformance/`).

The directory also carries worked-example graphs and demo scripts — not
protocol-server skeletons, but runnable templates plus a `demo.sh` (or
`main_e2e_test.go`) driving a real stack, each demonstrating one story from
`.ok-planner/design/stories.md` (see the `@story:` annotation in each
script/test):

| Directory / script | Story |
|---|---|
| `fanout-any-source/` | `fanout-any-substitution-source` |
| `fanout-fs-expand-folder/` | `fs-fanout-expand-folder` |
| `fanout-fs-list-array/` | `fanout-list-array` |
| `fanout-pg-list-array/` | `fanout-list-array` |
| `fanout-intent-inheritance/` | `fanout-intent-inheritance` |
| `inproc-loop-counter/` | `inproc-utility-executor` |
| `messages-as-nodes/` | `messages-as-nodes-substitution` |
| `park-resume/` (+ `park-resume-demo.sh`) | `bundled-park-resume-recipe` |
| `sub-claim-payload/` | `sub-claim-payload-substitution` |
| `cascade-send-demo.sh` + `cascade-send-demo-template.yaml` | `cascade-send` |
| `client-context-demo.sh` | `client-context` |
| `frame-origin-audit-demo.sh` + `frame-origin-audit-demo-template.yaml` | `frame-origin-audit` |
| `host-agent-control-plane-demo.sh` | `host-agent-control-plane` |
| `producer-error-demo.sh` | `producer-error-passthrough` |
| `subscription-mounting-demo.sh` | `subscription-mounting` |
| `onboarding-demo.sh` + `onboarding-template.yaml` | `operator-onboarding` (see below) |

## Sign-off validator (claude-agent gate)

A **sign-off validator** is not one of the core gRPC protocols above — it is a
validator-MCP server (or any out-of-band signer) that an agent consults to
cryptographically attest the claude-agent sign-off gate's bound output. The
claude-agent executor is flat Go (`lib/services/executors/claude-agent/`, no
`src/`, no TypeScript); there is no separate copyable validator package in
this tree. Build your validator against the wire contract itself:

- `lib/services/executors/claude-agent/signoff.go` — `BuildSignoffMessage`
  shows the exact bytes a validator signs
  (`SIGNOFF_DOMAIN ‖ "\n" ‖ dispatch_id ‖ "\n" ‖ canonical_json(value)`), and
  `VerifyRequiredSignoffs` is the executor's real verifier your signature must
  satisfy. It shows how to produce an Ed25519 signature and how to emit the
  PEM SPKI public key that `cli.required_signoffs[].public_key` carries.
- `lib/services/executors/claude-agent/testdata/signoff-wire-compat.json` —
  fixed cross-implementation test vectors (public key, canonicalized value,
  signature) that any conforming validator implementation, in any language,
  must reproduce; held in place by `signoff_test.go` against the executor's
  real verifier, not a fixture.

The executor's own test-only signer is an unexported helper inside
`signoff_test.go` (`testSigner`) — it exists solely to drive the executor's
internal tests and is not a copyable reference.

## Onboarding walkthrough (the first-steps demo)

`onboarding-template.yaml` + `onboarding-demo.sh` are the README's
first-steps walkthrough. Unlike `compose/template-a.yml` (which
references the placeholder `stub` executor), the onboarding template
references the bundled `verifier-shape-checks` executor and embeds a
real inline three-row dataset as JSON-schema `default:` values, so the
dispatch runs real verification work end-to-end without the operator
editing the file:

```sh
# Against a stack that declares the verifier-shape-checks executor:
rimsky run examples/onboarding-template.yaml
```

The demo script wraps `rimsky run` + `rimsky watch` so a single
invocation drives the full dev loop and exits 0 once the instance
terminates. The driver test under
`lib/services/test/scenarios/onboarding_demo_e2e_test.go` runs the
script as a subprocess against a testcontainers-managed stack with the
bundled `rimsky-executor-verifier-shape-checks` image wired in — the
harness resolves the image tag via `tools/image-src-tag.sh` (the same
content-addressed `src-<tree-hash>` derivation the Makefile uses), not
`:latest` — and is the load-bearing gate that the walkthrough actually
works.

For a bare-metal local stack, declare the verifier executor in
`rimsky.yml` (the all-in-one image ships with an empty executors block;
see `dockerfiles/all-in-one.rimsky.yml` for the default config shape):

```yaml
executors:
  verifier-shape-checks:
    transport: grpc
    endpoint: "verifier-shape-checks:9095"
    tls: off
    protocols: [executor]
```

Then `make service-images` (to build the verifier image locally) and
run both containers on the same docker network.

## Run a shipped TemplateSpec (the dev-loop verb)

`compose/template-a.yml` is a complete, minimal TemplateSpec (a `nodes:` block
dispatching one worker node to the `stub` executor). Against a stack you have
already brought up, the headline dev-loop verb registers, deploys, and
instantiates it in one shot, printing the new `instance_id`:

```sh
rimsky run examples/compose/template-a.yml
```

It is a real on-disk file — copy it, swap `stub` for your executor, and you
have a working first template without writing a TemplateSpec from scratch.

## Compose manifest

`compose/` is not a protocol server — it is a declarative `rimsky-compose.yml`
plus its two referenced TemplateSpecs (`template-a.yml`, `template-b.yml`).
`rimsky compose` is purely application-layer: it reconciles the declared
templates, tags, and instances against an **already-running** rimsky (it starts
nothing and invokes no infra command). Everything it manages is namespaced under
the reserved `compose:<project>:` tag prefix.

Against a stack you have already brought up:

```sh
rimsky compose up   -f examples/compose/rimsky-compose.yml
rimsky compose plan -f examples/compose/rimsky-compose.yml
rimsky compose down -f examples/compose/rimsky-compose.yml
```

## Building in-tree

This module is part of the workspace (`go.work`), so the repo gate covers it:
`go build ./...`, `go test ./...`, and `cd examples && golangci-lint run`.
