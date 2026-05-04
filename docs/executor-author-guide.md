# Executor Author Guide

> **v3 note:** the executor↔storage data path is unchanged from v2 —
> executors receive the store-supplied `address` from rimsky in the
> dispatch envelope and talk to the underlying storage directly. Under
> v3 stores-redesign, those addresses are produced by remote
> store-services rather than in-process Go code; from the executor's
> perspective nothing changes (the bytes are still opaque, still
> unwrapped per the executor's store-specific knowledge).
>
> Auth-blind advisory: rimsky has no machinery for credentials,
> encryption, or access control. Encrypt sensitive bytes before handing
> them to rimsky if you need protection.

This guide is for developers who want to implement a new rimsky executor —
in any language — and wire it into a rimsky deployment.

If you are operating an existing deployment, see `operator-guide.md`. For
the wire-format reference, see `protocol.md` (authoritative wire contract;
the proto source is `proto/v1/node_executor.proto`). For the conceptual
model — nodes, attributes, claims, named locks — see `node-graph-design.md`.
The vocabulary used throughout (claim, address, payload, region, intent,
alias, value-pass, claim-pass, etc.) is defined in `docs/glossary.md`.

---

## 1. The contract

An executor is a peer service. Rimsky supervisors open a stream per dispatch;
you emit zero or more `Heartbeat` events followed by **exactly one** terminal
event (`Complete`, `Blocked`, `Errored`, or `AsyncAccepted`); then you close
the stream.

That's the whole streaming contract. Rimsky owns retries, the state
machine, store commit/discard, attribute schema validation, and policy
chains. You own producing the work and reporting its outcome.

### 1.1 The RPC

```
service NodeExecutor {
  rpc Execute(ExecuteRequest) returns (stream ExecuteEvent);
}
```

### 1.2 What you receive

`ExecuteRequest` carries:

| field | purpose |
| --- | --- |
| `node_id`, `instance_id`, `node_type` | identifiers; echo them in logs and on callbacks |
| `userdata` | opaque per-node config from the template; **never substituted by rimsky** |
| `attributes` | per-run typed attribute object; source-directive fields are pre-populated by the supervisor at dispatch (read-only input); sourceless fields are slots for you to fill |
| `attributes_schema` | the JSON Schema for `attributes`, copied from the template, for executor-side reference |
| `stores` | map of `<alias> → StoreHandle` for every claim the node acquired or inherited; the supervisor calls `Store.Open` per claim inside the atomic acquisition transaction and packages the returned `Address` bytes opaquely |
| `callback_url` | base URL for both async-handoff terminal callbacks and incremental attribute writes (empty if the supervisor has no callback endpoint configured) |
| `cancel_token` | bearer token to echo on callbacks; the supervisor authenticates by matching it against the live dispatch row |
| `run_attempt` | 1-indexed retry counter, useful for idempotency keys. (There is no `resumed` flag — resume is universal; the store detects resumed-vs-fresh internally.) |

#### 1.2.1 `attributes` and `attributes_schema`

Attributes are the structured per-run data table — this is how rimsky
moves typed inputs into your executor and accepts typed outputs back.
The shape is declared by the template's `attributes.schema` and travels
verbatim as `ExecuteRequest.attributes_schema`.

At dispatch time the supervisor pre-populates fields whose schema declares
a `source:` directive. The full set of substitution paths your attributes
schema can use:

- `source: "{{deps.<node>.<field>}}"` — pulled from an upstream node's
  committed attributes (lifetime-independent — works after the upstream's
  claim has closed).
- `source: "{{claim.<alias>.address}}"` — the live claim's
  store-supplied address (opaque bytes; same shape your executor receives
  in the `stores` map). Requires the node to acquire `<alias>` itself OR
  `inherits:` it from an upstream acquirer.
- `source: "{{claim.<alias>.payload.<field>}}"` — a named field of the
  live claim's payload. Same validity rule.
- `source: "{{claim.<alias>.region}}"` — the live claim's region
  identifier (resolved selector or pick-policy-chosen item id). Same
  validity rule.
- `source: "{{params.<key>}}"` — pulled from the instance's params.

Source-directive fields are **read-only input**. Treat them as the
parameters of your run. Sourceless fields are slots for you to populate;
write them either via `Complete.attributes_delta` (terminal-final) or via
the incremental callback (§5). Rimsky never substitutes anything inside
`userdata`; substitution is exclusively an attributes-layer concern.

#### 1.2.2 `stores` map

Each entry in `stores` is keyed by the per-claim **alias** (defaults to
the store name; can be set explicitly when a node has multiple claims on
the same store). The value is a `StoreHandle` whose `handle` field is the
store-supplied `Address` bytes returned by `Store.Open`:

```
{
  handle: <store-supplied bytes, opaque to Rimsky>
}
```

The address shape is **per store kind** — opaque to Rimsky, decoded by
the executor per its store-specific knowledge of the `kind` declared
in operator config. The kind is not in the wire envelope (the executor
already knows which store kind backs each alias from the template + the
deployment's `rimsky.yml`); for tooling that needs the full picture, the
control-API exposes the operator config separately.

Reference v1 shapes:

| Kind | `handle` shape (illustrative) |
| --- | --- |
| `filesystem` | `{ "path": "/abs/path/to/locked/region" }` — open a directory the executor reads/writes with normal POSIX ops. |
| `postgres` | store-defined locator — typically a row or item identifier the executor can use against the operator-owned items table backing a configured pick policy. |

Future kinds (`s3`, `git`, etc.) follow the same shape pattern; see
`store-author-guide.md` once they ship.

The supervisor has already acquired each claim — and held it across any
inheritance — for the duration of your run. The orchestrator's
`rimsky_lock_holders` row is the authority on "is anyone holding this
claim"; the store enforces nothing extra. Stay within the regions
your template declared; writing or reading outside is undefined and may
collide with other in-flight nodes' acquisitions.

There is no `resumed` flag on `StoreHandle`. Resume is universal — the
store detects resumed-vs-fresh internally by lookup against its own
state, keyed by the lock-holder identity. Your executor doesn't need to
distinguish the two cases; the address it receives points at whatever
state the store considers current.

#### 1.2.3 `callback_url` and `cancel_token`

`callback_url` is the base URL of the supervisor's callback endpoint.
Append the right path for each callback shape:

- Incremental attribute write: `POST {callback_url}/v1/attributes/{node_id}`.
- Async-handoff terminal: `POST {callback_url}/v1/callback/{async_ack_id}`.

Both require `Authorization: Bearer <cancel_token>`. The supervisor
authenticates by comparing the token to the live dispatch row; mismatch
returns `401`. The token's lifetime is the dispatch — there is no
separate credential to store.

If `callback_url` is empty, the supervisor is not configured for
callbacks: do not emit `AsyncAccepted` and do not attempt incremental
writeback. Fall through to a synchronous `Complete` (with optional
`attributes_delta`) or report `Errored { error_class: "no_callback_configured" }`.

#### 1.2.4 Two propagation modes for downstream nodes

When a node's claim payload should reach a downstream node, the template
author has two options (spec §14.7 — pick per use case, not per
preference):

- **Value-pass.** The source node extracts captured fields into its own
  attributes via `source: "{{claim.<alias>.payload.<f>}}"` (or `.region`
  / `.address`); downstream nodes consume captured values via
  `{{deps.<source>.<field>}}`. **Lifetime-independent** — works after the
  source's claim has closed. Use this when the downstream only needs the
  data, not live access to the store-side state.
- **Claim-pass.** The downstream node declares `inherits: [{claim:
  <alias>}]` and substitutes via `{{claim.<alias>.address |
  payload.<f> | region}}`. **Requires the claim to remain open** — the
  inheriting node's existence holds it; the supervisor's auto-terminal
  mechanism fires resolution at holding-subgraph completion. Use this
  when the downstream needs live access to the store (read the
  picked file, write back to the locked region, etc.).

The "no hold + pass address" combination is structurally impossible:
`{{claim.<alias>.address}}` requires the alias to be acquired or
inherited, and inheritance extends the claim's lifetime.

#### 1.2.5 Encrypt-before-pass (operator practice)

Sensitive fields inside claim content (any of payload / address / region)
may be encrypted at the producer side before the bytes enter Rimsky's
address space (operator practice, not a Rimsky feature — Rimsky has no
encryption mechanism). Rimsky transports ciphertext as opaque bytes;
**executors decrypt at point of use**. Asymmetric is the recommended
default — your executor holds the private key; the producer (store,
admin tool, upstream pipeline) holds the public key. Encryption is
field-level, not whole-content, so Rimsky can still substitute by name.

This is a deployment-side practice, not a Rimsky feature. Document any
crypto your executor performs (which fields, which key material, which
algorithm) in your README so operators understand the boundary.

### 1.3 What you emit

| event | terminal? | meaning |
| --- | --- | --- |
| `Heartbeat` | no | "still working"; supervisor refreshes `last_heartbeat_at` |
| `Complete` | yes | success, with `changed`, `change_summary`, optional `attributes_delta` |
| `Blocked` | yes | can't progress without external help; maps to `executor_blocked` error class by default |
| `Errored` | yes | application-level failure, with `error_class` + `payload` |
| `AsyncAccepted` | yes | work accepted; outcome will arrive via `${callback_url}/v1/callback/{async_ack_id}` |

`Complete.attributes_delta` is your terminal-final attribute writeback —
a JSON object whose top-level keys merge into the node's attribute row.
Empty (or absent) when you used the incremental callback path. See §5.

There is **no `result` field** on terminal events. Your structured output
flows out via `attributes` (and any writes you make through store
handles). This is the load-bearing change from earlier rimsky: executors
no longer ship a free-form result blob; they populate typed slots in the
attribute schema.

### 1.4 Rules

1. **Exactly one terminal event per stream.** Emit it and then close.
2. **Close the stream after the terminal event.** Holding it open is a
   bug; supervisors that wait for stream-close for accounting need you to
   close promptly.
3. **Stream close without a terminal event is a protocol violation.** The
   supervisor maps it to `infra:transport_closed` and the conformance
   suite will fail you on it.
4. **Userdata is opaque.** Define whatever schema you want. Document it
   in your executor's README. Rimsky never parses or substitutes it.
5. **Source-directive attribute fields are read-only input.** Don't write
   into them — at best you'll overwrite upstream-derived state; at worst
   the supervisor's commit-time validation rejects the resulting shape.

---

## 2. Minimal Go example

See `executors/http-node/` for the reference implementation. The interesting
piece is `server.go`'s `executeCore`, a transport-independent function that
takes a `sendFunc` closure:

```go
type sendFunc func(*genv1.ExecuteEvent) error

func (s *Server) executeCore(ctx context.Context, req *genv1.ExecuteRequest, send sendFunc) error {
    _ = send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{
        TimestampMs: time.Now().UnixMilli(),
        Note:        "http-node starting",
    }}})

    // Read inputs from attributes (already substituted by rimsky).
    attrs := req.GetAttributes().AsMap()
    sourceID, _ := attrs["source_id"].(string)

    // Optionally read the locked region of a filesystem store.
    if h, ok := req.GetStores()["content"]; ok {
        path, _ := h.GetHandle().AsMap()["path"].(string)
        _ = path  // open files under path; respect write_regions / read_regions
    }

    // Do the work (HTTP fetch in this case, then build a structured delta).
    delta, err := structpb.NewStruct(map[string]any{
        "fetched_for": sourceID,
        "status":      200,
    })
    if err != nil {
        return err
    }

    return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
        Changed:         true,
        ChangeSummary:   "HTTP 200 OK",
        AttributesDelta: delta,
    }}})
}

func (s *Server) Execute(req *genv1.ExecuteRequest, stream genv1.NodeExecutor_ExecuteServer) error {
    return s.executeCore(stream.Context(), req, stream.Send)
}
```

The pattern — push the work into a transport-independent core and adapt both
gRPC and the HTTP+JSON bridge via a send closure — keeps all the branching
out of the protocol-aware surface. Copy it.

---

## 3. Minimal Python example

Python executors should use the **HTTP+JSON bridge** transport. It avoids
having to run `protoc` or wrangle generated gRPC stubs, and it's the path of
least resistance for sidecar executors. Rimsky supervisors speak both
transports interchangeably (see `protocol.md`).

This example uses FastAPI + uvicorn. Save as `executor.py` and run with
`uvicorn executor:app --host 0.0.0.0 --port 9091`.

```python
# executor.py — minimal rimsky Python executor (HTTP+JSON bridge)
#
# Protocol: POST /v1/Execute with a JSON body matching rimsky.v1.ExecuteRequest.
# Respond with application/x-ndjson: one JSON-encoded ExecuteEvent per line.
# Emit >=0 heartbeats + EXACTLY ONE terminal event, then close the response.

import json
import os
import time

from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse, JSONResponse

app = FastAPI()
STUB_MODE = os.environ.get("RIMSKY_EXECUTOR_STUB_MODE", "0") == "1"


class BadInputError(ValueError):
    """Raised when userdata or attributes are missing required fields."""


def _event(ev: dict) -> bytes:
    """Serialize one ExecuteEvent as a single ndjson line."""
    return (json.dumps(ev) + "\n").encode()

def _heartbeat(note: str) -> bytes:
    return _event({"heartbeat": {"timestampMs": int(time.time() * 1000), "note": note}})

def _complete(attributes_delta=None, changed=True, summary="") -> bytes:
    payload = {"changed": changed, "changeSummary": summary}
    if attributes_delta is not None:
        payload["attributesDelta"] = attributes_delta
    return _event({"complete": payload})

def _errored(error_class: str, message: str) -> bytes:
    return _event({"errored": {"errorClass": error_class, "payload": {"error": message}}})

@app.get("/health")
def health():
    return {"status": "ok", "stub_mode": STUB_MODE}

@app.post("/v1/Execute")
async def execute(req: Request):
    try:
        body = await req.json()
    except Exception as exc:
        return JSONResponse({"error": f"bad json: {exc}"}, status_code=400)

    userdata   = body.get("userdata", {}) or {}
    attributes = body.get("attributes", {}) or {}
    stores     = body.get("stores", {}) or {}

    def stream():
        yield _heartbeat("python-executor starting")
        if STUB_MODE:
            yield _complete(
                attributes_delta=userdata.get("stub_response", {"stub": True}),
                changed=True,
                summary="stub",
            )
            return
        # --- real work goes here ---
        try:
            delta = do_the_work(userdata, attributes, stores)
        except BadInputError as exc:
            yield _errored("invalid_input", str(exc))
            return
        except RuntimeError as exc:
            yield _errored("execution_failed", str(exc))
            return
        yield _complete(attributes_delta=delta, changed=True, summary="ok")

    return StreamingResponse(stream(), media_type="application/x-ndjson")


# --- your application logic below ---

def do_the_work(userdata: dict, attributes: dict, stores: dict) -> dict:
    """Replace with your real executor logic. Return a JSON-serializable dict
    keyed by attribute names — these merge into the node's attribute row.
    Source-directive attribute fields are read-only input; write only into
    sourceless slots."""
    target = userdata.get("target")
    if not target:
        raise BadInputError("userdata.target required")
    source_id = attributes.get("source_id")
    if not source_id:
        raise BadInputError("attributes.source_id required (declared as source-directive in template)")
    # Optionally use a filesystem store handle:
    #   path = stores["content"]["handle"]["path"]
    #   ... open files under path ...
    return {"echoed_target": target, "echoed_source": source_id}
```

**`requirements.txt`:**

```
fastapi>=0.110
uvicorn[standard]>=0.29
```

**Run it:**

```bash
RIMSKY_EXECUTOR_STUB_MODE=1 uvicorn executor:app --host 0.0.0.0 --port 9091
```

**Register with the supervisor** by adding an entry to
`supervisor-config.yml`:

```yaml
executors:
  - name: my-python-executor
    transport: http
    endpoint: "http://localhost:9091/v1/Execute"
    concurrency: 4
```

### 3.1 Running the conformance suite against the Python example

With `RIMSKY_EXECUTOR_STUB_MODE=1` set, the executor above passes all
conformance scenarios:

```bash
rimsky-conformance \
  --endpoint http://localhost:9091/v1/Execute \
  --transport http \
  --require-stub-mode
```

If you omit `--require-stub-mode` the suite will warn that the executor
might be talking to real dependencies, and some scenarios (e.g.
`malformed_userdata`) may not make sense — stub mode is the conformance
contract.

### 3.2 Why HTTP+JSON and not gRPC from Python?

Nothing prevents a Python executor from speaking gRPC — `grpcio` plus the
generated stubs work fine. The HTTP+JSON bridge is preferred for three
reasons:

1. No codegen step, no committed `_pb2.py` files, no `grpc_tools.protoc` in
   your build.
2. Streaming responses are natively supported by every HTTP client and
   web framework worth using.
3. The rimsky supervisor supports both transports uniformly; you pay no
   correctness or performance cost for choosing HTTP.

---

## 4. Minimal TypeScript example

See `executors/claude-agent/` for the canonical TypeScript reference. Key
points:

- Uses gRPC (`@grpc/grpc-js` + generated stubs) because the agent streams
  long-running token output and benefits from gRPC's built-in flow control.
- Uses the **incremental attributes callback** (§5) and the **async
  handoff** (§6) — the agent is long-lived and benefits from durable
  partial state plus survival across executor restarts.
- Same contract as every other executor: emit heartbeats as tokens arrive,
  then exactly one terminal event.
- Stub mode via `CLAUDE_STUB_MODE=1` short-circuits to a deterministic
  Complete, for CI and conformance.

If you are writing an executor in TypeScript/Node and your workload does NOT
need fine-grained streaming, the HTTP+JSON bridge is equally valid — wire it
with `express` or `fastify` and emit newline-delimited JSON events on the
response stream.

---

## 5. Incremental attribute writeback

Most executors are fine with the terminal-final pattern: accumulate the
result in memory, emit `Complete { attributes_delta: {...} }`, close. The
incremental pattern is for executors that benefit from each write being
durable before the run terminates — agentic loops where the run might be
interrupted and resumed; long-running pipelines that want progress
visibility; any executor whose work stream produces structured outputs
incrementally.

### 5.1 The endpoint

```
POST  ${callback_url}/v1/attributes/{node_id}
Content-Type: application/json
Authorization: Bearer <cancel_token>

{
  "delta": {
    "<field_name>": <value>,
    "<other_field>": <value>,
    ...
  }
}
```

`{node_id}` is `ExecuteRequest.node_id` verbatim. The body's `delta` is a
shallow object whose top-level keys merge into `rimsky_node_attributes.data`
(later writes overwrite earlier writes on key collision). Values are
arbitrary JSON.

### 5.2 Responses

- `204 No Content` — write applied.
- `401 Unauthorized` — bearer token missing/wrong, or the URL `node_id`
  does not match the live dispatch row that the token resolves to.
- `400 Bad Request` — body malformed: bad JSON, missing `delta`, or
  non-object `delta`.
- `404 Not Found` — no dispatch for `node_id` is currently `running` on
  this supervisor (the original supervisor restarted, the heartbeat
  sweep released the claim, etc.).
- `500 Internal Server Error` — supervisor-internal problem; safe to retry.

### 5.3 Validation timing

The supervisor does **not** validate against `attributes_schema` on each
incremental write. Schema validation runs once at terminal commit. You're
free to write a partial state mid-run that wouldn't pass schema, as long
as the cumulative state at terminal time is valid.

### 5.4 Combining with terminal-final

You can use only the incremental path, only `Complete.attributes_delta`,
or both. Final committed state is the shallow merge of:

```
dispatch-resolved attributes
  ∪ incremental writes (in arrival order; later wins on collision)
  ∪ Complete.attributes_delta (last; wins on collision)
```

The `claude-agent` executor uses the incremental path exclusively and
emits `Complete { attributes_delta: null }` to signal "incremental writes
are authoritative."

### 5.5 Ordering and idempotency

Each POST is processed atomically and in arrival order. Concurrent POSTs
from the same executor instance are not specified beyond "ordering may
not be preserved across overlapping in-flight requests." Serialize your
incremental writes per node.

A duplicate write of the same key (network retry, etc.) overwrites with
the same value — idempotent in the trivial sense. There is no
compare-and-swap primitive.

---

## 6. Async handoff

Use `AsyncAccepted` when the real work runs outside your executor process
(queue worker, batch job, third-party webhook, agentic subprocess that
might outlive the executor). The flow:

1. Your `Execute` handler accepts the request, kicks off the work, and
   returns `AsyncAccepted { async_ack_id, expected_completion_ms }` —
   this is a **terminal** event from the supervisor's perspective; close
   the stream.
2. The supervisor holds the dispatch claim, keeps the node in `running`,
   and persists the lock-holder rows and `rimsky_node_attributes` across
   the async window. It schedules a heartbeat-loss watchdog per its
   configured cutoff.
3. The work runs elsewhere. During this time you MAY issue zero or more
   incremental attribute writes via §5.
4. When the work completes, your system POSTs the terminal outcome to
   `${callback_url}/v1/callback/{async_ack_id}` with the `cancel_token`
   bearer.

### 6.1 The terminal-callback endpoint

```
POST  ${callback_url}/v1/callback/{async_ack_id}
Content-Type: application/json
Authorization: Bearer <cancel_token>

{
  "type": "complete" | "blocked" | "errored",

  "changed":           <bool>,         // complete
  "change_summary":    "<str>",        // complete
  "attributes_delta":  <obj | null>,   // complete; null/absent = incremental-only

  "reason":            "<str>",        // blocked
  "context":           <json>,         // blocked

  "error_class":       "<str>",        // errored
  "payload":           <json>          // errored
}
```

The body is **flat and keyed by `type`** — distinct from the streaming
`ExecuteEvent` oneof shape. Only the fields relevant to the selected
`type` need be populated; unknown fields are ignored.
`attributes_delta: null` (or omitted) is the explicit "incremental
writeback was used; no terminal-final delta" signal — the supervisor
commits whatever incremental writes accumulated during the running
window.

### 6.2 Responses

- `200 OK` with `{"status":"accepted"}` — callback applied.
- `401 Unauthorized` — bearer token did not match the live dispatch
  claim.
- `404 Not Found` — `async_ack_id` does not match an outstanding async
  handoff registered by this supervisor (original supervisor restarted,
  heartbeat sweep released the claim before the callback arrived, or
  the executor is confused).
- `400 Bad Request` — body malformed.
- `500 Internal Server Error` — supervisor-internal problem. The ack is
  re-registered so you may retry with idempotent backoff.

### 6.3 Rules

- If `callback_url` is empty in the request, **do not** return
  `AsyncAccepted` — there is nowhere for the callback to go. Fall back to
  synchronous execution or return
  `Errored { error_class: "no_callback_configured" }`.
- The callback POST is not retried automatically by the supervisor;
  duplicate callbacks for a successfully-applied `async_ack_id` will
  return 404 (the registry removed it on success). On `500` the ack is
  re-registered and you should retry.
- The supervisor still applies its heartbeat-loss cutoff while waiting.
  If the callback takes longer than the configured cutoff, the node will
  be marked timed-out and the claim released. Use `expected_completion_ms`
  in `AsyncAccepted` to hint at wait duration, but the supervisor's
  cutoff is authoritative.

### 6.4 When to use async handoff

Use it when:

- The work's runtime is heavy-tailed (LLM calls, external API waits,
  human-in-the-loop approvals).
- The executor wants to survive its own restart while work continues
  (e.g. by kicking off a Kubernetes job and polling status).
- Holding a transport-level connection would be expensive or unreliable.

Don't use it when:

- The work is deterministic and bounded (< 30s). Synchronous `Complete`
  is simpler.
- You can't guarantee the callback will fire. The supervisor's
  heartbeat-loss sweep will eventually release the claim, but
  "eventually" depends on cutoff config.

---

## 7. Error reporting

### 7.1 Application-level errors: `Errored`

Use `Errored { error_class, payload }` for application-level failures —
things the template author can anticipate and configure a policy chain for.
Templates bind policy chains to specific `error_class` strings:

```yaml
error_types:
  http_unexpected_status:
    policy:
      - { action: retry, count: 2, backoff: exponential, base_delay_ms: 30000 }
      - { action: give_up }
```

**Pick stable, machine-readable class names.** They are API surface — once
operators start configuring policies against them, you cannot rename them
without breaking templates. Treat `error_class` like HTTP status codes: a
small, stable set documented in your README.

If the class is not declared in the node's `error_types`, the supervisor
treats it as `give_up` with an unknown-class reason.

### 7.2 External blockage: `Blocked`

`Blocked { reason, context }` is for when the executor can detect that no
amount of retry will help — a missing secret, a closed upstream account, a
permission error. Rimsky maps it to the `executor_blocked` class by
default; templates can bind policy to it explicitly, and an executor may
also choose a more specific declared class instead of `Blocked` if the
template-author distinguishes those cases.

### 7.3 Infrastructure errors

Do NOT use `Errored` for your own bugs. If your executor crashes
mid-stream, the supervisor classifies that separately (stream close
without terminal → `infra:transport_closed`; panic-caught → executor-side
infra error). The operator's policy for infrastructure errors is
orthogonal to the application-level policy chain.

### 7.4 Payload shape

Both `Errored.payload` and `Blocked.context` accept any JSON value. Use
them to attach debug context — request IDs, upstream response codes, retry
headers. The supervisor stores the payload on the event log verbatim.

### 7.5 Schema-validation failures

If you write attributes that don't match the declared schema (either via
`Complete.attributes_delta` or via incremental callback), the
supervisor's commit-time validation raises `attributes_schema_failed`,
which routes through the policy chain like any other error. You can
catch this earlier by validating against `ExecuteRequest.attributes_schema`
client-side before emitting; rimsky validates regardless.

---

## 8. Userdata

Userdata is your executor's API surface. Rimsky never parses, validates,
or template-substitutes it. Whatever shape you define is delivered to
`Execute` byte-for-byte as supplied in the template.

### 8.1 No template substitution inside userdata

Substitution is exclusively an attributes-layer concern. Operators who
want a value to flow into a node from upstream / claim / params declare it
in `attributes.schema` with a `source:` directive. **Never** inside
`userdata`.

If your executor wants a template-like substitution mechanism (e.g.
referring to attribute values inside a prompt template), implement it
yourself on top of `ExecuteRequest.attributes` — and document the syntax
clearly in your README. Reject malformed templates with
`Errored { error_class: "userdata_template_error" }`.

### 8.2 Validate on receipt

Validate `ExecuteRequest.userdata` against your schema as the first thing
you do. Reject malformed input with `Blocked` or `Errored` rather than
`Complete` — the conformance suite tests this explicitly.

---

## 9. Stub mode

Every executor **must** support a stub mode that short-circuits all real
network / filesystem / subprocess calls and returns a deterministic
terminal event. Stub mode is required for:

- The conformance suite (runs against stub mode by default).
- CI test scenarios (rimsky's scenario harness drives executors in stub
  mode so tests are deterministic).
- Offline dev environments (no API keys, no outbound calls).

### 9.1 How to wire it

- Read `RIMSKY_EXECUTOR_STUB_MODE` at process start. `"1"` = stub mode;
  anything else = real mode.
- In stub mode, `Execute` should emit an opening heartbeat, then a
  `Complete` with `attributes_delta = userdata.stub_response` (if set) or
  a sensible default (e.g. `{"stub": true}`), and close.
- Optional: a flag `--require-stub-mode` to the conformance runner will
  verify your executor probe reports stub mode; you can expose this via a
  `/health` endpoint returning `{"stub_mode": true}`.

### 9.2 Why an env var and not userdata?

Stub mode is a **process-level** property, controlled by the operator
running the executor. Letting callers switch it per-request via userdata
would leak test behavior into production. The env var ensures the decision
is made once, at deploy time.

---

## 10. Running the conformance suite

```bash
rimsky-conformance --endpoint <url> --transport <grpc|http> [--require-stub-mode]
```

Scenarios include:

- `execute_happy_path` — basic Execute + terminal + clean stream close
- `heartbeats` — heartbeats arrive before terminal
- `terminal_is_last` — no events after the terminal
- `stream_close_without_terminal` — detected and reported as violation
- `malformed_userdata` — executor handles bad userdata gracefully
- `attributes_serialization` — `Complete.attributes_delta` round-trips a
  structured JSON object unchanged across encoder boundaries
- `attributes_callback` — incremental writes apply, including 401/404
  paths for bad bearer tokens and unknown node IDs
- `async_handoff` — `AsyncAccepted` + callback loop (if callback configured),
  including bearer-token auth
- `cancel` — executor responds to ctx cancellation
- `unknown_ack_id` — terminal callbacks with unknown IDs are rejected

A passing `--require-stub-mode` run is the acceptance gate for merging a
new executor into a rimsky deployment.

---

## 11. Docker image conventions

Convention, not enforcement:

| aspect | recommendation |
| --- | --- |
| base image | matched to the executor's runtime; multi-stage if you compile |
| exposed ports | one port for the protocol (gRPC and/or HTTP bridge), one for metrics/health |
| env vars | prefix with `RIMSKY_EXECUTOR_<NAME>_*` for process-local config; `RIMSKY_EXECUTOR_STUB_MODE` is shared |
| signals | handle SIGTERM by draining in-flight Executes up to a grace period, then exit |
| logs | structured JSON to stderr |
| health | `/health` on the metrics port returning `{status, stub_mode, ...}` |

Publish images as `rimsky/executor-<name>:<version>`. The compose file at
`deploy/docker-compose.yml` and the Helm chart at
`deploy/kubernetes/rimsky-chart/` both use this naming convention.

---

## 12. Supervisor integration

Once your executor image runs, the operator adds it to their
`supervisor-config.yml`:

```yaml
executors:
  - name: my-python-executor      # matches template node.executor
    transport: http               # or "grpc"
    endpoint: "http://python-executor:9091/v1/Execute"
    concurrency: 8                # max concurrent dispatches the supervisor will send
```

Templates reference it by `name` and pass opaque `userdata` plus a typed
attribute schema:

```yaml
nodes:
  - type: my-task
    executor: my-python-executor
    attributes:
      schema:
        type: object
        properties:
          source_id: { type: string, source: "{{params.source_id}}" }
          result:    { type: string }
        required: [source_id, result]
    userdata:
      target: "https://example.com/api"
      # whatever schema your executor defines
```

When the supervisor picks up a dispatch row whose node's `executor` field
matches a configured name, it pre-populates source-directive attribute
fields, acquires any declared store regions / claims, and opens an
`Execute` stream to the registered endpoint. No further wiring is needed.

---

## 13. Checklist

Before shipping your executor, verify:

- [ ] Exactly one terminal event per Execute stream.
- [ ] Stream closes after the terminal event.
- [ ] `userdata` validated on receipt; malformed input → `Blocked` /
      `Errored`, never `Complete`.
- [ ] Source-directive `attributes` fields treated as read-only; only
      sourceless slots written.
- [ ] Store handles used only within declared `write_regions` /
      `read_regions`.
- [ ] `RIMSKY_EXECUTOR_STUB_MODE=1` short-circuits without real side effects.
- [ ] `error_class` names are documented and stable.
- [ ] Async handoff (if used): `AsyncAccepted` only when `callback_url`
      is non-empty; terminal POST to
      `${callback_url}/v1/callback/{async_ack_id}` with
      `Authorization: Bearer <cancel_token>`.
- [ ] Incremental attribute writeback (if used): POST to
      `${callback_url}/v1/attributes/{node_id}` with
      `Authorization: Bearer <cancel_token>` and `{"delta": {...}}` body.
- [ ] `rimsky-conformance --require-stub-mode` passes all scenarios.
- [ ] Metrics/health endpoint exposed.
- [ ] Docker image published at `rimsky/executor-<name>:<version>`.
- [ ] README documents the userdata schema and error classes.


## Observability protocol (optional)

Per `docs/specs/2026-05-02-dashboard-and-observability-design.md` §2,
executors MAY implement `proto/v1/executor_observability.proto` to
expose per-dispatch traces to dashboards. The dispatch protocol is
unchanged.

The service exposes three RPCs:

- `GetCapabilities` — declares which sub-features the executor supports.
- `GetTrace(dispatch_id)` — snapshot of all retained events.
- `StreamTrace(dispatch_id)` — replay-then-live stream, closes with a
  synthetic `category: "trace_complete"` event when the dispatch
  terminals.

### Capabilities-only "no observability" pattern

The simplest implementation declares no observability and rejects the
RPCs:

```go
func (*MyExecObservability) GetCapabilities(_ context.Context, _ *genv1.GetCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
    return &genv1.ObservabilityCapabilities{}, nil // all flags false; retention 0
}
func (*MyExecObservability) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
    return nil, status.Error(codes.Unimplemented, "no trace surface")
}
```

This satisfies the `--check-observability` conformance probe trivially.
See `executors/stub/observability.go` for the reference Go impl and
`executors/claude-agent/src/observability.ts` for the TS impl
(planned).

### Standard vocabulary

The dashboard renders standard categories with bespoke widgets. Spec
§2.4 enumerates the v1 set: `step_started`, `step_completed`,
`step_failed`, `subcall_started`, `subcall_completed`, `tool_call`,
`log`, `error`, `trace_complete`. Required `attributes` keys per
category are listed in spec §2.4 — the conformance probe validates
their presence. Free-form `category` strings are first-class; the
dashboard renders them as plain log lines.

### Retention semantics

`retention_after_terminal_seconds` declares how long the executor
retains a completed dispatch's trace. Eviction returns `Trace{evicted:
true, complete: true, events: []}`. During the active window the
executor MUST be able to serve the full event stream.

### Streaming semantics

`StreamTrace` replays history then streams new events. Executors MAY
implement it as snapshot-then-hold (send the snapshot, hold connection
until terminal or 5min timeout, send `trace_complete`, close). The
conformance probe accepts that strategy.

### Custom UI hook

Optional. `CustomUI.ui_url` is opaque to Rimsky and the dashboard. It
can point to the executor's embedded UI, a sidecar, or any external
service. `dispatch_url_template` markers for executors are
`{dispatch_id}`, `{instance_id}`, `{node_type}`. `embed_mode` chooses
between `LINK`, `IFRAME`, or `BOTH`.

### Inert-userdata invariant

The executor's trace is not constrained by blessed invariant 11. The
executor knows what its own `userdata` means; it MAY include
`userdata`-derived information in trace event attributes if it wants
to. Rimsky never sees the trace data — the dashboard fetches it from
the executor directly, never proxied through Rimsky core.

### Capabilities probe (`--check-observability`)

`rimsky-conformance --check-observability` calls `GetCapabilities`,
validates the proto shape, and (when retention ≤ 60s) probes the
eviction sub-shape. See spec §6 for the full probe contract.

Reference: `proto/v1/executor_observability.proto`,
`executors/http-node/observability.go`,
`executors/stub/observability.go`.
