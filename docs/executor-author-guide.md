# Executor Author Guide

This guide is for developers who want to implement a new rimsky executor —
in any language — and wire it into a rimsky deployment.

If you are operating an existing deployment, see `operator-guide.md`. For
the wire-format reference, see `protocol.md`. For the concept model, see
`node-graph-design.md`.

---

## 1. The contract

An executor is a peer service. Rimsky supervisors open a stream per dispatch;
you emit zero or more `Heartbeat` events followed by **exactly one** terminal
event (`Complete`, `Blocked`, `Errored`, or `AsyncAccepted`); then you close
the stream.

That's the whole contract. Everything else — retries, state machines,
resource versioning, error-class policy chains — is rimsky's problem.

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
| `node_id`, `instance_id`, `node_type` | identifiers; echo them in logs |
| `userdata` | opaque per-node config from the template; **this is your API surface** |
| `instance_params` | the instance's params, post-redaction |
| `deps_data` | current version of each dep resource, keyed by dep's `node_type` |
| `reads_data` | current version of each `reads_resources` entry, keyed by declared name |
| `callback_url` | where to POST async-handoff callbacks (empty if none) |

### 1.3 What you emit

| event | terminal? | meaning |
| --- | --- | --- |
| `Heartbeat` | no | "still working"; supervisor refreshes `last_heartbeat_at` |
| `Complete` | yes | success, with `result`, `changed`, `change_summary` |
| `Blocked` | yes | can't progress without human help; maps to `executor_blocked` error class |
| `Errored` | yes | application-level failure, with `error_class` + `payload` |
| `AsyncAccepted` | yes | work accepted but outcome will come via `callback_url` |

### 1.4 Rules

1. **Exactly one terminal event per stream.** Emit it and then close.
2. **Close the stream after the terminal event.** Clients that keep the
   stream open indefinitely are treated as infrastructure errors.
3. **Stream close without a terminal event is a protocol violation.** The
   supervisor maps it to `executor_stream_closed_without_terminal` and the
   conformance suite will fail you on it.
4. **Userdata is opaque to rimsky.** Define whatever schema you want. Document
   it in your executor's README.

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
    // ... do the work ...
    return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
        Result:        resultValue,
        Changed:       true,
        ChangeSummary: "HTTP 200 OK",
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
    """Raised when userdata is missing required fields."""


def _event(ev: dict) -> bytes:
    """Serialize one ExecuteEvent as a single ndjson line."""
    return (json.dumps(ev) + "\n").encode()

def _heartbeat(note: str) -> bytes:
    return _event({"heartbeat": {"timestampMs": int(time.time() * 1000), "note": note}})

def _complete(result, changed=True, summary="") -> bytes:
    return _event({"complete": {"result": result, "changed": changed, "changeSummary": summary}})

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
    userdata = body.get("userdata", {}) or {}

    def stream():
        yield _heartbeat("python-executor starting")
        if STUB_MODE:
            yield _complete(userdata.get("stub_response", {"stub": True}),
                            changed=True, summary="stub")
            return
        # --- real work goes here ---
        try:
            result = do_the_work(userdata)
        except BadInputError as exc:
            yield _errored("invalid_userdata", str(exc))
            return
        except RuntimeError as exc:
            yield _errored("execution_failed", str(exc))
            return
        yield _complete(result, changed=True, summary="ok")

    return StreamingResponse(stream(), media_type="application/x-ndjson")


# --- your application logic below ---

def do_the_work(userdata: dict) -> dict:
    """Replace with your real executor logic. Must return a JSON-serializable
    dict. Raise BadInputError for invalid userdata; RuntimeError for runtime
    failures you want mapped to error_class='execution_failed'."""
    target = userdata.get("target")
    if not target:
        raise BadInputError("userdata.target required")
    return {"echoed_target": target}
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
- Same contract as every other executor: emit heartbeats as tokens arrive,
  then exactly one terminal event.
- Stub mode via `CLAUDE_STUB_MODE=1` short-circuits to a deterministic
  Complete, for CI and conformance.

If you are writing an executor in TypeScript/Node and your workload does NOT
need fine-grained streaming, the HTTP+JSON bridge is equally valid — wire it
with `express` or `fastify` and emit newline-delimited JSON events on the
response stream.

---

## 5. Async handoff

Use `AsyncAccepted` when the real work runs outside your executor process
(queue worker, batch job, third-party webhook). The flow:

1. Your `Execute` handler accepts the request, kicks off the work, and
   returns `AsyncAccepted { async_ack_id }` — this is a **terminal** event
   from the supervisor's perspective; close the stream.
2. The work runs elsewhere; when complete, your system POSTs a JSON body
   to `callback_url` (from `ExecuteRequest.callback_url`). Body shape:

   ```json
   {
     "async_ack_id": "same-id-you-returned",
     "event": {
       "complete": {
         "result": {...},
         "changed": true,
         "changeSummary": "done"
       }
     }
   }
   ```

3. The supervisor correlates by `async_ack_id`, commits the result, and
   transitions the node.

Rules:

- If `callback_url` is empty in the request, **do not** return
  `AsyncAccepted` — there is nowhere for the callback to go. Fall back to
  synchronous execution or return `Errored { error_class: "no_callback_configured" }`.
- The callback POST should be idempotent: the supervisor ignores
  duplicate callbacks for the same `async_ack_id`.
- The supervisor still applies heartbeat-loss cutoff while waiting. If the
  callback takes longer than 2× supervisor heartbeat interval, the node
  will be marked `heartbeat_timeout` and the claim released. Use
  `expected_completion_ms` in `AsyncAccepted` to hint at wait duration, but
  the supervisor's cutoff is authoritative.

---

## 6. Error reporting

### 6.1 Application-level errors: `Errored`

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

### 6.2 External blockage: `Blocked`

`Blocked { reason, context }` is for when the executor can detect that no
amount of retry will help — a missing secret, a closed upstream account, a
permission error. Rimsky maps it to the `executor_blocked` class; templates
can bind policy to it explicitly.

### 6.3 Infrastructure errors

Do NOT use `Errored` for your own bugs. If your executor crashes
mid-stream, the supervisor classifies that separately (stream close without
terminal = infrastructure error; panic-caught = executor-side infra error).
The operator's policy for infrastructure errors is orthogonal to the
application-level policy chain.

### 6.4 Payload shape

Both `Errored.payload` and `Blocked.context` accept any JSON value. Use
them to attach debug context — request IDs, upstream response codes, retry
headers. The supervisor stores the payload on the event log verbatim.

---

## 7. Stub mode

Every executor **must** support a stub mode that short-circuits all real
network / filesystem / subprocess calls and returns a deterministic
terminal event. Stub mode is required for:

- The conformance suite (runs against stub mode by default).
- CI test scenarios (rimsky's scenario harness drives executors in stub
  mode so tests are deterministic).
- Offline dev environments (no API keys, no outbound calls).

### 7.1 How to wire it

- Read `RIMSKY_EXECUTOR_STUB_MODE` at process start. `"1"` = stub mode;
  anything else = real mode.
- In stub mode, `Execute` should emit an opening heartbeat, then a
  `Complete` with `result = userdata.stub_response` (if set) or a
  sensible default (e.g. `{"stub": true}`), and close.
- Optional: a flag `--require-stub-mode` to the conformance runner will
  verify your executor probe reports stub mode; you can expose this via a
  `/health` endpoint returning `{"stub_mode": true}`.

### 7.2 Why an env var and not userdata?

Stub mode is a **process-level** property, controlled by the operator
running the executor. Letting callers switch it per-request via userdata
would leak test behavior into production. The env var ensures the decision
is made once, at deploy time.

---

## 8. Running the conformance suite

```bash
rimsky-conformance --endpoint <url> --transport <grpc|http> [--require-stub-mode]
```

Scenarios include:

- `execute_happy_path` — basic Execute + terminal + clean stream close
- `heartbeats` — heartbeats arrive before terminal
- `terminal_is_last` — no events after the terminal
- `stream_close_without_terminal` — detected and reported as violation
- `malformed_userdata` — executor handles bad userdata gracefully
- `result_serialization` — `Complete.result` is a valid `google.protobuf.Value`
- `async_handoff` — `AsyncAccepted` + callback loop (if callback configured)
- `cancel` — executor responds to ctx cancellation
- `unknown_ack_id` — callbacks with unknown IDs are rejected

A passing `--require-stub-mode` run is the acceptance gate for merging a
new executor into a rimsky deployment.

---

## 9. Docker image conventions

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

## 10. Supervisor integration

Once your executor image runs, the operator adds it to their
`supervisor-config.yml`:

```yaml
executors:
  - name: my-python-executor      # matches template node.executor
    transport: http               # or "grpc"
    endpoint: "http://python-executor:9091/v1/Execute"
    concurrency: 8                # max concurrent dispatches the supervisor will send
```

Templates reference it by `name`:

```yaml
nodes:
  - type: my-task
    executor: my-python-executor
    userdata:
      # whatever schema you defined
```

When the supervisor picks up a dispatch row whose node's `executor` field
matches a configured name, it opens an `Execute` stream to the registered
endpoint. No further wiring is needed.

---

## 11. Checklist

Before shipping your executor, verify:

- [ ] Exactly one terminal event per Execute stream.
- [ ] Stream closes after terminal event.
- [ ] `RIMSKY_EXECUTOR_STUB_MODE=1` short-circuits without real side effects.
- [ ] `error_class` names are documented and stable.
- [ ] `callback_url` handling is correct (use AsyncAccepted only when it's
      non-empty; POST includes `async_ack_id`).
- [ ] `rimsky-conformance --require-stub-mode` passes all scenarios.
- [ ] Metrics/health endpoint exposed.
- [ ] Docker image published at `rimsky/executor-<name>:<version>`.
- [ ] README documents the userdata schema and error classes.
