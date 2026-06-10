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

## Sign-off validator (claude-agent gate)

A **sign-off validator** is not one of the core gRPC protocols above — it is a
validator-MCP server (or any out-of-band signer) that an agent consults to
cryptographically attest the claude-agent sign-off gate's bound output. Because
the gate's byte-contract is defined in TypeScript (the claude-agent executor),
the copyable reference validator lives in that package's non-dist source tree:

- `lib/services/executors/claude-agent/src/examples/signoff-validator/reference-validator.ts`
  — Apache-2.0, **copy-and-modify**. It shows the exact bytes a validator signs
  (`SIGNOFF_DOMAIN ‖ "\n" ‖ dispatch_id ‖ "\n" ‖ canonical_json(value)`), how to
  produce an Ed25519 signature the executor's real `verifyRequiredSignoffs`
  accepts, and how to emit the PEM SPKI public key that
  `cli.required_signoffs[].public_key` carries. Its correctness is proven in the
  repo gate by `reference-validator.test.ts` against the executor's **real**
  verifier — not by a fixture.

Contrast this with `lib/services/executors/claude-agent/src/signoff-test-signer.ts`,
which is the executor's own **test-only** signer: it is excluded from `dist/` via
`tsconfig.json`, exists solely to drive the executor's internal tests, and is
**not** a copyable reference. Build your validator from the Apache reference
above, not from the dist-excluded test signer.

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
bundled `rimsky-executor-verifier-shape-checks:latest` image wired in,
and is the load-bearing gate that the walkthrough actually works.

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
